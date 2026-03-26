package api

import (
	"net/http"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
)

func handleHealth(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	return map[string]interface{}{
		"status": "healthy", "timestamp": time.Now().Format(time.RFC3339), "project": s.Config.GoogleCloudProject,
		"version": infra.Version, "commit": infra.Commit,
	}, nil
}

// handleMetrics serves an empty Prometheus-format response for the GMP sidecar scraper.
// The app does not expose custom metrics; this prevents 404 warnings from the sidecar.
func handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
}
