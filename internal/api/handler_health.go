package api

import (
	"net/http"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func handleHealth(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	return map[string]interface{}{
		"status": "healthy", "timestamp": time.Now().Format(time.RFC3339), "project": s.Config.GoogleCloudProject,
		"version": infra.Version, "commit": infra.Commit,
	}, nil
}

// handleMetrics serves Prometheus exposition format for Google Cloud Prometheus (and any Prometheus scraper).
// Exempt from wrapAPI: delegates to promhttp which writes its own content type and body.
func handleMetrics(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK)
	promhttp.Handler().ServeHTTP(w, r)
}
