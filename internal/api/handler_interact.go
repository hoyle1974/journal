package api

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
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
	app, hasApp := s.App.(*infra.App)

	if s.Config != nil && s.Config.DebugReportEnabled && hasApp && !result.Error {
		asyncCtx := context.WithoutCancel(ctx)
		toolCalls := result.ToolCalls
		debugLogs := result.DebugLogs
		answer := result.Answer
		q := question
		app.SubmitAsync(func() {
			narrative := agent.GenerateDebugReport(asyncCtx, app, q, toolCalls, debugLogs, answer)
			if narrative != "" {
				infra.LoggerFrom(asyncCtx).Debug("query debug report", "narrative", narrative)
			}
		})
	}
	infra.LoggerFrom(ctx).Info("query completed",
		"error", result.Error, "iterations", result.Iterations,
		"tool_call_count", len(result.ToolCalls),
		"answer_preview", utils.TruncateString(result.Answer, 120))
	return result, nil
}
