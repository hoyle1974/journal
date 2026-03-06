package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

func handleProcessEntry(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	infra.LoggerFrom(ctx).Debug("process-entry handler: request received", "method", r.Method)
	if r.Method != http.MethodPost {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		UUID      string `json:"uuid"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}
	if data.UUID == "" || data.Content == "" || data.Source == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "uuid, content, and source are required"})
		return
	}
	infra.LoggerFrom(ctx).Debug("process-entry handler: invoking ProcessEntry", "uuid", data.UUID, "source", data.Source, "content_preview", utils.TruncateString(data.Content, 50))
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := s.Backend.ProcessEntry(ctx, data.UUID, data.Content, data.Timestamp, data.Source); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	infra.LoggerFrom(ctx).Debug("process-entry handler: done", "uuid", data.UUID)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleSaveQuery(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	infra.LoggerFrom(ctx).Debug("save-query handler: request received", "method", r.Method)
	if r.Method != http.MethodPost {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
		Source   string `json:"source"`
		IsGap    bool   `json:"is_gap"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}
	if data.Question == "" || data.Source == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "question and source are required"})
		return
	}
	infra.LoggerFrom(ctx).Debug("save-query handler: saving to queries collection", "question_preview", utils.TruncateString(data.Question, 60), "is_gap", data.IsGap)
	if _, err := s.Backend.SaveQuery(ctx, data.Question, data.Answer, data.Source, data.IsGap); err != nil {
		infra.LoggerFrom(ctx).Error("save-query failed", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	infra.LoggerFrom(ctx).Info("save-query", "question", utils.TruncateString(data.Question, 50))
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleDraftTools(s *Server, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	drafts, err := s.Backend.GetDraftTools(r.Context())
	if err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"drafts": drafts})
}

func handleDraftToolApply(s *Server, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		UUID string `json:"uuid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	if err := s.Backend.MarkToolDraftApplied(r.Context(), data.UUID); err != nil {
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
