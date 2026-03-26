package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
)

// processStartTime is recorded once at init so /metrics can expose process_start_time_seconds.
var processStartTime = time.Now()

func handleHealth(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	return map[string]interface{}{
		"status": "healthy", "timestamp": time.Now().Format(time.RFC3339), "project": s.Config.GoogleCloudProject,
		"version": infra.Version, "commit": infra.Commit,
	}, nil
}

// handleMetrics serves a minimal Prometheus-format response for the GMP sidecar scraper.
// process_start_time_seconds prevents the sidecar "start_time metric is missing" warning.
func handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "# HELP process_start_time_seconds Start time of the process since unix epoch in seconds.\n")
	fmt.Fprintf(w, "# TYPE process_start_time_seconds gauge\n")
	fmt.Fprintf(w, "process_start_time_seconds %g\n", float64(processStartTime.UnixNano())/1e9)
}
