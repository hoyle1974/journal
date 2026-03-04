package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

func init() {
	functions.HTTP("JotAPI", JotAPI)
	startRateLimitCleanup()
	// Initialize app at cold start so JotAPI can use it; if this fails, first request will get 500.
	if err := InitDefaultApp(context.Background()); err != nil {
		log.Printf("init default app failed: %v", err)
	}
}

// Public routes that don't require API key auth
var publicRoutes = map[string]bool{
	"":                      true,
	"/health":               true,
	"/metrics":              true,
	"/webhook":              true,
	"/sms":                  true, // Twilio webhook (validated separately)
	"/privacy-policy":       true,
	"/terms-and-conditions": true,
}

func checkAuth(app *App, r *http.Request) (int, string) {
	if JotAPIKey == "" {
		app.Logger.Warn("no JOT_API_KEY configured, allowing unauthenticated access")
		return 0, ""
	}

	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" {
		return http.StatusUnauthorized, "Missing X-API-Key header"
	}

	if apiKey != JotAPIKey {
		app.Logger.Warn("invalid API key attempted", "path", r.URL.Path)
		return http.StatusForbidden, "Invalid API key"
	}

	return 0, ""
}

// JotAPI is the main entry point for the cloud function.
func JotAPI(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()
	// Extract trace context from incoming headers (Traceparent, etc.) so internal callers get a linked trace.
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(r.Header))

	app, err := getOrCreateApp(ctx)
	if err != nil {
		Logger.Error("app not available", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	ctx = WithApp(ctx, app)

	// Ensure all background work completes before function returns
	defer app.WaitForBackgroundTasks()

	// When client sends X-Want-Trace-Id (e.g. jot --trace), force this trace to be sampled and exported.
	if r.Header.Get("X-Want-Trace-Id") == "true" {
		ctx = WithForceTrace(ctx)
	}

	// Start request span
	ctx, span := StartSpan(ctx, "http.request")
	defer span.End()

	path := strings.TrimSuffix(r.URL.Path, "/")
	method := r.Method

	span.SetAttributes(map[string]string{
		"http.method": method,
		"http.path":   path,
	})

	// Check auth for protected routes
	if !publicRoutes[path] {
		if code, msg := checkAuth(app, r); code != 0 {
			LogRequest(ctx, method, path, code, time.Since(startTime))
			writeJSON(w, code, map[string]string{"error": msg})
			return
		}
	}

	// Rate limit by IP (protects expensive endpoints even if API key is compromised)
	if !checkRateLimit(r, path) {
		app.Logger.Warn("rate limit exceeded", "path", path, "ip", getClientIP(r))
		LogRequest(ctx, method, path, http.StatusTooManyRequests, time.Since(startTime))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "Rate limit exceeded. Please try again later.",
		})
		return
	}

	// Create a response wrapper to capture status code
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	rw.Header().Set("X-Trace-Id", span.TraceID())
	rw.Header().Set("X-Cloud-Project", GoogleCloudProject)

	// Route to appropriate handler
	switch {
	case path == "" || path == "/health":
		handleHealth(rw, r.WithContext(ctx))
	case path == "/metrics":
		handleMetrics(rw, r.WithContext(ctx))
	case path == "/privacy-policy":
		handlePrivacyPolicy(rw, r.WithContext(ctx))
	case path == "/terms-and-conditions":
		handleTermsAndConditions(rw, r.WithContext(ctx))
	case path == "/log":
		handleLog(rw, r.WithContext(ctx))
	case path == "/query":
		handleQuery(rw, r.WithContext(ctx))
	case path == "/entries" || strings.HasPrefix(path, "/entries/"):
		handleEntries(rw, r.WithContext(ctx), path)
	case path == "/sync":
		handleSync(rw, r.WithContext(ctx))
	case path == "/dream":
		handleDream(rw, r.WithContext(ctx))
	case path == "/janitor":
		handleJanitor(rw, r.WithContext(ctx))
	case path == "/rollup":
		handleRollup(rw, r.WithContext(ctx))
	case path == "/pending-questions":
		handlePendingQuestions(rw, r.WithContext(ctx))
	case strings.HasPrefix(path, "/pending-questions/"):
		if id, suffix, ok := parsePendingQuestionPath(path); ok && suffix == "resolve" {
			handlePendingQuestionResolve(rw, r.WithContext(ctx), id)
		} else {
			writeJSON(rw, http.StatusNotFound, map[string]string{"error": "Not found"})
		}
	case path == "/webhook":
		handleWebhook(rw, r.WithContext(ctx))
	case path == "/sms":
		handleSMS(rw, r.WithContext(ctx))
	case path == "/plan":
		handlePlan(rw, r.WithContext(ctx))
	case path == "/decay-contexts":
		handleDecayContexts(rw, r.WithContext(ctx))
	case path == "/backfill-embeddings":
		handleBackfillEmbeddings(rw, r.WithContext(ctx))
	case path == "/internal/process-entry":
		handleProcessEntry(rw, r.WithContext(ctx))
	case path == "/internal/save-query":
		handleSaveQuery(rw, r.WithContext(ctx))
	default:
		writeJSON(rw, http.StatusNotFound, map[string]interface{}{
			"error": "Not found",
			"path":  path,
			"available_routes": []string{
				"GET  /health",
				"GET  /metrics",
				"GET  /privacy-policy",
				"GET  /terms-and-conditions",
				"POST /log",
				"POST /query",
				"POST /plan",
				"GET  /entries",
				"POST /sync",
				"POST /dream",
				"POST /janitor",
				"POST /rollup",
				"POST /webhook",
				"POST /sms",
				"POST /decay-contexts",
				"POST /backfill-embeddings",
				"GET  /pending-questions",
				"POST /pending-questions/:id/resolve",
			},
		})
	}

	// Log the request
	duration := time.Since(startTime)
	LogRequest(ctx, method, path, rw.statusCode, duration)
	span.SetAttributes(map[string]string{
		"http.status_code": fmt.Sprintf("%d", rw.statusCode),
	})
}

// parsePendingQuestionPath parses "/pending-questions/{id}/resolve" into (id, "resolve", true). Otherwise returns ("", "", false).
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

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
