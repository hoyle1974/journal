package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/persona"
	"github.com/jackstrohm/jot/pkg/utils"
)

const logMultipartMaxBytes = 10 << 20 // 10MB

func handleLog(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	var content, source string
	var timestamp *string
	var imageBytes []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(logMultipartMaxBytes); err != nil {
			infra.LoggerFrom(ctx).Warn("log multipart parse error", "error", err)
			return nil, handlerError(http.StatusBadRequest, "invalid multipart form")
		}
		content = strings.TrimSpace(r.FormValue("content"))
		if content == "" {
			content = strings.TrimSpace(r.FormValue("text"))
		}
		source = strings.TrimSpace(r.FormValue("source"))
		if ts := r.FormValue("timestamp"); ts != "" {
			timestamp = &ts
		}
		if f, _, err := r.FormFile("image"); err == nil {
			defer f.Close()
			imageBytes, _ = io.ReadAll(f)
		}
	} else {
		var data struct {
			Content   string  `json:"content" validate:"required"`
			Source    string  `json:"source"`
			Timestamp *string `json:"timestamp"`
		}
		if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
			infra.LoggerFrom(ctx).Warn("log request decode/validate error", "error", err)
			return nil, handlerError(http.StatusBadRequest, err.Error())
		}
		content = strings.TrimSpace(data.Content)
		source = data.Source
		timestamp = data.Timestamp
	}
	if content == "" {
		return nil, handlerError(http.StatusBadRequest, "content cannot be only whitespace")
	}
	if source == "" {
		source = "api"
	}
	LogHandlerRequest(ctx, r.Method, path, "source", source, "content_length", len(content), "has_image", len(imageBytes) > 0)
	infra.EntriesTotal.Inc()
	entryUUID, err := s.Agent.AddEntry(ctx, content, source, timestamp, imageBytes)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("entry failed", "error", err)
		return nil, err
	}
	return map[string]interface{}{"success": true, "uuid": entryUUID, "message": "Entry logged successfully"}, nil
}

// handleQuery runs the FOH and delivers the answer to the API client as JSON.
// SMS callers use a Cloud Task (process-sms-query) which runs FOH and delivers the answer via SendSMS.
func handleQuery(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	var data struct {
		Question string `json:"question" validate:"required"`
		Source   string `json:"source"`
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		return nil, handlerError(http.StatusBadRequest, err.Error())
	}
	question := strings.TrimSpace(data.Question)
	source := data.Source
	if source == "" {
		source = "api"
	}
	LogHandlerRequest(ctx, r.Method, path, "question_preview", utils.TruncateString(question, 80), "source", source)
	result := s.Agent.RunQuery(ctx, question, source)
	if result.Answer != "" && !result.Error {
		if app, ok := s.App.(*infra.App); ok {
			result.Answer = persona.Apply(ctx, app, result.Answer, question)
		}
	}
	infra.LoggerFrom(ctx).Info("query completed",
		"error", result.Error, "iterations", result.Iterations,
		"tool_call_count", len(result.ToolCalls),
		"answer_preview", utils.TruncateString(result.Answer, 120))
	return result, nil
}

func handlePlan(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	var data struct {
		Goal string `json:"goal" validate:"required"`
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		return nil, handlerError(http.StatusBadRequest, err.Error())
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
		return nil, handlerError(code, err.Error())
	}
	return map[string]interface{}{"success": true, "plan": result}, nil
}
