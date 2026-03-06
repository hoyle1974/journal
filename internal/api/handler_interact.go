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
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
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
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "Invalid JSON")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}
	content := strings.TrimSpace(data.Content)
	source := data.Source
	if source == "" {
		source = "api"
	}
	if content == "" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "content is required")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}
	LogHandlerRequest(ctx, r.Method, path, "source", source, "content_length", len(content))
	infra.EntriesTotal.Inc()
	entryUUID, err := s.Agent.AddEntry(ctx, content, source, data.Timestamp)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("entry failed", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "success", true, "uuid", entryUUID)
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "uuid": entryUUID, "message": "Entry logged successfully"})
}

func handleQuery(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		Question string `json:"question"`
		Source   string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "Invalid JSON")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	question := strings.TrimSpace(data.Question)
	source := data.Source
	if source == "" {
		source = "api"
	}
	if question == "" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "question is required")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "question is required"})
		return
	}
	LogHandlerRequest(ctx, r.Method, path, "question_preview", utils.TruncateString(question, 80), "source", source)
	result := s.Agent.RunQuery(ctx, question, source)
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK,
		"error", result.Error, "iterations", result.Iterations, "tool_call_count", len(result.ToolCalls),
		"answer_preview", utils.TruncateString(result.Answer, 120))
	WriteJSON(w, http.StatusOK, result)
}

func handlePlan(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		Goal string `json:"goal"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "Invalid JSON")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	goal := strings.TrimSpace(data.Goal)
	if goal == "" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "goal is required")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "goal is required"})
		return
	}
	LogHandlerRequest(ctx, r.Method, path, "goal_preview", utils.TruncateString(goal, 80))
	result, err := s.Agent.CreateAndSavePlan(ctx, goal)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("plan failed", "error", err)
		code := http.StatusInternalServerError
		if infra.IsLLMQuotaOrBillingError(err) {
			code = http.StatusTooManyRequests
		} else if infra.IsLLMPermissionOrBillingDenied(err) {
			code = http.StatusForbidden
		}
		LogHandlerResponse(ctx, r.Method, path, code, "error", err.Error())
		WriteJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "success", true, "plan_length", len(result))
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "plan": result})
}
