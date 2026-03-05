package jot

import (
	"net/http"
	"time"

	"github.com/jackstrohm/jot/internal/api"
)

func handleHealth(s *api.Server, w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"project":   s.Config.GoogleCloudProject,
	})
}

func handleMetrics(s *api.Server, w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"queries_total":      QueriesTotal.Value(),
		"entries_total":      EntriesTotal.Value(),
		"tool_calls_total":   ToolCallsTotal.Value(),
		"gemini_calls_total": GeminiCallsTotal.Value(),
		"errors_total":       ErrorsTotal.Value(),
		"timestamp":          time.Now().Format(time.RFC3339),
	})
}
