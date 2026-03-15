package api

import (
	"net/http"

	"github.com/jackstrohm/jot/internal/static"
	"github.com/jackstrohm/jot/internal/infra"
)

func handlePrivacyPolicy(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	infra.LoggerFrom(ctx).Info("handler response", "event", "http_response", "method", r.Method, "path", path, "status", http.StatusOK, "content_type", "text/html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(static.PrivacyPolicyHTML))
}

func handleTermsAndConditions(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	infra.LoggerFrom(ctx).Info("handler response", "event", "http_response", "method", r.Method, "path", path, "status", http.StatusOK, "content_type", "text/html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(static.TermsAndConditionsHTML))
}
