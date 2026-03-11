package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackstrohm/jot/pkg/infra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// PublicRoutes are paths that don't require API key auth.
var PublicRoutes = map[string]bool{
	"": true, "/health": true, "/metrics": true, "/webhook": true, "/sms": true,
	"/privacy-policy": true, "/terms-and-conditions": true,
}

// Router is the full request handler: prelude (trace, app, span, auth, rate limit) then path dispatch.
func Router(s *Server, w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(r.Header))
	ctx = s.App.WithContext(ctx)
	defer s.App.WaitForBackgroundTasks()
	if r.Header.Get("X-Want-Trace-Id") == "true" {
		ctx = infra.WithForceTrace(ctx)
	}
	ctx, span := infra.StartSpan(ctx, "http.request")
	defer span.End()
	path := strings.TrimSuffix(r.URL.Path, "/")
	method := r.Method
	span.SetAttributes(map[string]string{"http.method": method, "http.path": path})
	infra.LoggerFrom(ctx).Debug("request started", "event", "request_start", "method", method, "path", path, "trace_id", span.TraceID())
	if !PublicRoutes[path] {
		if code, msg := CheckAuth(s, r); code != 0 {
			infra.LogRequest(ctx, method, path, code, time.Since(startTime))
			WriteJSON(w, code, map[string]string{"error": msg})
			return
		}
	}
	if !CheckRateLimit(r, path) {
		s.Logger.Warn("rate limit exceeded", "path", path, "ip", GetClientIP(r))
		infra.LogRequest(ctx, method, path, http.StatusTooManyRequests, time.Since(startTime))
		WriteJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Rate limit exceeded. Please try again later."})
		return
	}
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	rw.Header().Set("X-Trace-Id", span.TraceID())
	rw.Header().Set("X-Cloud-Project", s.Config.GoogleCloudProject)
	reqWithCtx := r.WithContext(ctx)
	switch {
	case path == "" || path == "/health":
		handleHealth(s, rw, reqWithCtx)
	case path == "/metrics":
		handleMetrics(s, rw, reqWithCtx)
	case path == "/privacy-policy":
		handlePrivacyPolicy(s, rw, reqWithCtx)
	case path == "/terms-and-conditions":
		handleTermsAndConditions(s, rw, reqWithCtx)
	case path == "/log":
		handleLog(s, rw, reqWithCtx)
	case path == "/query":
		handleQuery(s, rw, reqWithCtx)
	case path == "/entries" || strings.HasPrefix(path, "/entries/"):
		handleEntries(s, rw, reqWithCtx, path)
	case path == "/sync":
		handleSync(s, rw, reqWithCtx)
	case path == "/dream/latest":
		handleDreamLatest(s, rw, reqWithCtx)
	case path == "/dream/status":
		handleDreamStatus(s, rw, reqWithCtx)
	case path == "/dream":
		handleDream(s, rw, reqWithCtx)
	case path == "/janitor":
		handleJanitor(s, rw, reqWithCtx)
	case path == "/rollup":
		handleRollup(s, rw, reqWithCtx)
	case path == "/pending-questions":
		handlePendingQuestions(s, rw, reqWithCtx)
	case strings.HasPrefix(path, "/pending-questions/"):
		if id, suffix, ok := parsePendingQuestionPath(path); ok && suffix == "resolve" {
			handlePendingQuestionResolve(s, rw, reqWithCtx, id)
		} else {
			WriteJSON(rw, http.StatusNotFound, map[string]string{"error": "Not found"})
		}
	case path == "/webhook":
		handleWebhook(s, rw, reqWithCtx)
	case path == "/sms":
		handleSMS(s, rw, reqWithCtx)
	case path == "/plan":
		handlePlan(s, rw, reqWithCtx)
	case path == "/decay-contexts":
		handleDecayContexts(s, rw, reqWithCtx)
	case path == "/backfill-embeddings":
		handleBackfillEmbeddings(s, rw, reqWithCtx)
	case path == "/internal/process-entry":
		handleProcessEntry(s, rw, reqWithCtx)
	case path == "/internal/process-sms-query":
		handleProcessSMSQuery(s, rw, reqWithCtx)
	case path == "/internal/save-query":
		handleSaveQuery(s, rw, reqWithCtx)
	case path == "/internal/dream-run":
		handleDreamRun(s, rw, reqWithCtx)
	default:
		infra.LoggerFrom(ctx).Info("handler response", "event", "http_response", "method", method, "path", path, "status", http.StatusNotFound, "error", "Not found")
		WriteJSON(rw, http.StatusNotFound, map[string]interface{}{
			"error": "Not found", "path": path,
			"available_routes": []string{
				"GET  /health", "GET  /metrics", "GET  /privacy-policy", "GET  /terms-and-conditions",
				"POST /log", "POST /query", "POST /plan", "GET  /entries", "POST /sync", "GET  /dream/latest", "GET  /dream/status", "POST /dream",
				"POST /janitor", "POST /rollup", "POST /webhook", "POST /sms", 				"POST /decay-contexts",
				"POST /backfill-embeddings", "GET  /pending-questions", "POST /pending-questions/:id/resolve",
			},
		})
	}
	if rw.latencyBreakdown != nil {
		ctx = infra.WithLatencyBreakdown(ctx, rw.latencyBreakdown)
	}
	infra.LogRequest(ctx, method, path, rw.statusCode, time.Since(startTime))
	span.SetAttributes(map[string]string{"http.status_code": fmt.Sprintf("%d", rw.statusCode)})
}

func parsePendingQuestionPath(path string) (id, suffix string, ok bool) {
	const prefix = "/pending-questions/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		return "", "", false
	}
	id = parts[0]
	if len(parts) == 2 {
		suffix = strings.TrimSuffix(parts[1], "/")
	}
	return id, suffix, true
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

// CheckAuth returns (0, "") if authorized, or (statusCode, message) if not.
func CheckAuth(s *Server, r *http.Request) (int, string) {
	if s.Config.JotAPIKey == "" {
		s.Logger.Warn("no JOT_API_KEY configured, allowing unauthenticated access")
		return 0, ""
	}
	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" {
		return http.StatusUnauthorized, "Missing X-API-Key header"
	}
	if apiKey != s.Config.JotAPIKey {
		s.Logger.Warn("invalid API key attempted",
			"path", r.URL.Path, "method", r.Method, "ip", GetClientIP(r),
			"user_agent", r.UserAgent(), "key_length", len(apiKey))
		return http.StatusForbidden, "Invalid API key"
	}
	return 0, ""
}
