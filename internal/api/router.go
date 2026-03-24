package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackstrohm/jot/internal/infra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// NewRouter builds a chi.Mux with global middleware and route groups (public and protected).
func NewRouter(s *Server) *chi.Mux {
	r := chi.NewRouter()

	r.Use(serverCtxMiddleware(s))
	r.Use(traceMiddleware)
	r.Use(responseWriterMiddleware)
	r.Use(logRequestMiddleware)

	// Public routes (no auth)
	r.Group(func(r chi.Router) {
		r.Get("/", wrapAPI(handleHealth))
		r.Get("/health", wrapAPI(handleHealth))
		r.Get("/privacy-policy", wrap(handlePrivacyPolicy))
		r.Get("/terms-and-conditions", wrap(handleTermsAndConditions))
		r.Post("/telegram", wrap(handleTelegram))
	})

	// Protected routes (auth required)
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware(s))
		r.Post("/log", wrapAPI(handleLog))
		r.Post("/ingest", wrapAPI(handleIngest))
		r.Post("/query", wrapAPI(handleQuery))
		r.Get("/entries", wrapAPI(handleEntries))
		r.Get("/entries/{uuid}", wrapAPI(handleEntries))
		r.Patch("/entries", wrapAPI(handleEntries))
		r.Patch("/entries/{uuid}", wrapAPI(handleEntries))
		r.Delete("/entries", wrapAPI(handleEntries))
		r.Delete("/entries/{uuid}", wrapAPI(handleEntries))
		r.Get("/pending-questions", wrapAPI(handlePendingQuestions))
		r.Post("/pending-questions/{id}/resolve", wrapAPI(handlePendingQuestionResolve))
		r.Post("/backfill-embeddings", wrapAPI(handleBackfillEmbeddings))
		r.Post("/internal/process-entry", wrapAPI(handleProcessEntry))
		r.Post("/internal/process-telegram-query", wrapAPI(handleProcessTelegramQuery))
		r.Post("/internal/save-query", wrapAPI(handleSaveQuery))
		r.Post("/internal/replay", wrapAPI(handleReplay))
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		path := pathForLog(r.URL.Path)
		method := r.Method
		infra.LoggerFrom(ctx).Info("handler response", "event", "http_response", "method", method, "path", path, "status", http.StatusNotFound, "error", "Not found")
		WriteJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "Not found", "path": path,
			"available_routes": []string{
				"GET  /health", "GET  /privacy-policy", "GET  /terms-and-conditions",
				"POST /log", "POST /query", "GET  /entries", "POST /telegram",
				"POST /backfill-embeddings", "GET  /pending-questions", "POST /pending-questions/:id/resolve",
			},
		})
	})

	return r
}

// wrap converts a handler that takes (s *Server, w, r) into an http.HandlerFunc by reading Server from context.
func wrap(f func(*Server, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := ServerFromContext(r.Context())
		if s == nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		f(s, w, r)
	}
}


func serverCtxMiddleware(s *Server) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(r.Header))
			ctx = s.App.WithContext(ctx)
			ctx = contextWithServer(ctx, s)
			defer s.App.WaitForBackgroundTasks()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func contextWithServer(ctx context.Context, s *Server) context.Context {
	return context.WithValue(ctx, serverContextKey{}, s)
}

func traceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.Header.Get("X-Want-Trace-Id") == "true" {
			ctx = infra.WithForceTrace(ctx)
		}
		ctx, span := infra.StartSpan(ctx, "http.request")
		defer span.End()
		path := pathForLog(r.URL.Path)
		method := r.Method
		span.SetAttributes(map[string]string{"http.method": method, "http.path": path})
		infra.LoggerFrom(ctx).Debug("request started", "event", "request_start", "method", method, "path", path, "trace_id", span.TraceID())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func responseWriterMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		ctx := r.Context()
		if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
			rw.Header().Set("X-Trace-Id", span.SpanContext().TraceID().String())
		}
		if s := ServerFromContext(ctx); s != nil {
			rw.Header().Set("X-Cloud-Project", s.Config.GoogleCloudProject)
		}
		next.ServeHTTP(rw, r)
	})
}

func logRequestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := w.(*responseWriter)
		next.ServeHTTP(w, r)
		ctx := r.Context()
		if rw.latencyBreakdown != nil {
			ctx = infra.WithLatencyBreakdown(ctx, rw.latencyBreakdown)
		}
		path := pathForLog(r.URL.Path)
		infra.LogRequest(ctx, r.Method, path, rw.statusCode, time.Since(start))
		if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
			span.SetAttributes(attribute.String("http.status_code", fmt.Sprintf("%d", rw.statusCode)))
		}
	})
}

func authMiddleware(s *Server) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.Config.JotAPIKey == "" {
				s.Logger.Warn("no JOT_API_KEY configured, allowing unauthenticated access")
				next.ServeHTTP(w, r)
				return
			}
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "Missing X-API-Key header"})
				return
			}
			if apiKey != s.Config.JotAPIKey {
				s.Logger.Warn("invalid API key attempted",
					"path", r.URL.Path, "method", r.Method,
					"user_agent", r.UserAgent(), "key_length", len(apiKey))
				WriteJSON(w, http.StatusForbidden, map[string]string{"error": "Invalid API key"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode       int
	latencyBreakdown *infra.LatencyBreakdown
}

// SetLatencyBreakdown stores a latency breakdown so the router can include it in "request completed" logs.
func (rw *responseWriter) SetLatencyBreakdown(b *infra.LatencyBreakdown) {
	rw.latencyBreakdown = b
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// WriteJSON sets Content-Type and status and encodes data as JSON.
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
