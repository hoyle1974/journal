package api

import (
	"net/http"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
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
		Content   string  `json:"content" validate:"required"`
		Source    string  `json:"source"`
		Timestamp *string `json:"timestamp"`
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		infra.LoggerFrom(ctx).Warn("log request decode/validate error", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", err.Error())
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	content := strings.TrimSpace(data.Content)
	if content == "" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "content cannot be only whitespace")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "content cannot be only whitespace"})
		return
	}
	source := data.Source
	if source == "" {
		source = "api"
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

// handleQuery runs the FOH and delivers the answer to the API client as JSON.
// SMS callers use a Cloud Task (process-sms-query) which runs FOH and delivers the answer via SendSMS.
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
		Question string `json:"question" validate:"required"`
		Source   string `json:"source"`
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", err.Error())
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	question := strings.TrimSpace(data.Question)
	source := data.Source
	if source == "" {
		source = "api"
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
		Goal string `json:"goal" validate:"required"`
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", err.Error())
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	goal := strings.TrimSpace(data.Goal)
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
