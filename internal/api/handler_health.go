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
