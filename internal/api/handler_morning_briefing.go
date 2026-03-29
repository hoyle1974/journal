package api

import (
	"encoding/json"
	"net/http"
)

type morningBriefingRequest struct {
	Force bool `json:"force"`
}

// handleMorningBriefing triggers the Morning Briefing agent cycle.
// POST /internal/morning-briefing  body: {"force": true|false}
func handleMorningBriefing(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	var req morningBriefingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return nil, handlerError(http.StatusBadRequest, "invalid request body")
	}
	result, err := s.Agent.RunMorningBriefing(r.Context(), req.Force)
	if err != nil {
		return nil, err
	}
	return result, nil
}
