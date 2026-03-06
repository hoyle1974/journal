package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

func handleLog(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	infra.LoggerFrom(ctx).Debug("log handler: request received", "method", r.Method)
	if r.Method != http.MethodPost {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		Content   string  `json:"content"`
		Source    string  `json:"source"`
		Timestamp *string `json:"timestamp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		infra.LoggerFrom(ctx).Warn("log request decode error", "error", err)
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}
	content := strings.TrimSpace(data.Content)
	source := data.Source
	if source == "" {
		source = "api"
	}
	if content == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}
	infra.LoggerFrom(ctx).Debug("log handler: adding entry", "source", source, "content_preview", utils.TruncateString(content, 60))
	infra.EntriesTotal.Inc()
	entryUUID, err := s.Backend.AddEntry(ctx, content, source, data.Timestamp)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("entry failed", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	infra.LoggerFrom(ctx).Info("entry logged", "uuid", entryUUID, "source", source, "content", utils.TruncateString(content, 50))
	infra.LoggerFrom(ctx).Debug("log handler: done", "uuid", entryUUID, "reason", "entry saved to Firestore")
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "uuid": entryUUID, "message": "Entry logged successfully"})
}

func handleQuery(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	infra.LoggerFrom(ctx).Debug("query handler: request received", "method", r.Method)
	if r.Method != http.MethodPost {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		Question string `json:"question"`
		Source   string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	question := strings.TrimSpace(data.Question)
	source := data.Source
	if source == "" {
		source = "api"
	}
	if question == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "question is required"})
		return
	}
	infra.LoggerFrom(ctx).Info("query", "question", utils.TruncateString(question, 80), "source", source)
	infra.LoggerFrom(ctx).Debug("query handler: invoking FOH agent", "question_preview", utils.TruncateString(question, 100))
	result := s.Backend.RunQuery(ctx, question, source)
	if result.Error {
		infra.LoggerFrom(ctx).Error("query error", "answer", result.Answer)
		infra.LoggerFrom(ctx).Debug("query handler: done with error", "iterations", result.Iterations, "tool_calls", len(result.ToolCalls))
	} else {
		infra.LoggerFrom(ctx).Info("query done", "answer", utils.TruncateString(result.Answer, 120))
		infra.LoggerFrom(ctx).Debug("query handler: done successfully", "iterations", result.Iterations, "tool_calls", len(result.ToolCalls), "forced_conclusion", result.ForcedConclusion)
	}
	WriteJSON(w, http.StatusOK, result)
}

func handlePlan(s *Server, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		Goal string `json:"goal"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	goal := strings.TrimSpace(data.Goal)
	if goal == "" {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "goal is required"})
		return
	}
	ctx := r.Context()
	infra.LoggerFrom(ctx).Info("plan started", "goal", utils.TruncateString(goal, 60))
	infra.LoggerFrom(ctx).Debug("plan handler: invoking CreateAndSavePlan", "goal_preview", utils.TruncateString(goal, 80))
	result, err := s.Backend.CreateAndSavePlan(ctx, goal)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("plan failed", "error", err)
		code := http.StatusInternalServerError
		if s.Backend.IsLLMQuotaOrBillingError(err) {
			code = http.StatusTooManyRequests
		} else if s.Backend.IsLLMPermissionOrBillingDenied(err) {
			code = http.StatusForbidden
		}
		WriteJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	infra.LoggerFrom(ctx).Info("plan completed")
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "plan": result})
}
