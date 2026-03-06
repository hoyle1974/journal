package api

import (
	"net/http"
	"time"

	"github.com/jackstrohm/jot/pkg/infra"
)

func handleHealth(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "healthy")
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status": "healthy", "timestamp": time.Now().Format(time.RFC3339), "project": s.Config.GoogleCloudProject,
	})
}

func handleMetrics(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK)
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"queries_total": infra.QueriesTotal.Value(), "entries_total": infra.EntriesTotal.Value(),
		"tool_calls_total": infra.ToolCallsTotal.Value(), "gemini_calls_total": infra.GeminiCallsTotal.Value(),
		"errors_total": infra.ErrorsTotal.Value(), "timestamp": time.Now().Format(time.RFC3339),
	})
}
