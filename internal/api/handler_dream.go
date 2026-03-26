package api

import (
	"encoding/json"
	"net/http"
)

type dreamRequest struct {
	Force bool `json:"force"`
}

// handleDream triggers the Dreamer background cycle.
// POST /internal/dream  body: {"force": true|false}
func handleDream(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	var req dreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return nil, handlerError(http.StatusBadRequest, "invalid request body")
	}
	result, err := s.Agent.RunDreamer(r.Context(), req.Force)
	if err != nil {
		return nil, err
	}
	return result, nil
}
