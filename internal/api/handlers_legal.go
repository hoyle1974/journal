package api

import (
	"net/http"

	"github.com/jackstrohm/jot/internal/static"
)

func handlePrivacyPolicy(s *Server, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(static.PrivacyPolicyHTML))
}

func handleTermsAndConditions(s *Server, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(static.TermsAndConditionsHTML))
}
