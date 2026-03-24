package service

import (
	"context"

	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

func queryResultToAPI(r *agent.QueryResult) *api.QueryResult {
	if r == nil {
		return nil
	}
	return &api.QueryResult{
		Answer:           r.Answer,
		Iterations:       r.Iterations,
		ToolCalls:        r.ToolCalls,
		ForcedConclusion: r.ForcedConclusion,
		Error:            r.Error,
		DebugLogs:        r.DebugLogs,
	}
}

// AgentService handles agent and cron operations for the API.
type AgentService struct {
	app *infra.App
}

// NewAgentService returns an AgentService for use with api.Server. app is the runtime app (Firestore, Gemini, etc.).
func NewAgentService(app *infra.App) *AgentService {
	return &AgentService{app: app}
}

// AddEntry adds an entry and enqueues processing. imageBytes is optional; when non-empty, uploads to GCS and stores image_url on the entry.
func (a *AgentService) AddEntry(ctx context.Context, content, source string, timestamp *string, imageBytes []byte) (string, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "AddEntry", "source", source, "content_length", len(content), "has_image", len(imageBytes) > 0)
	var imageURL string
	if len(imageBytes) > 0 {
		var err error
		imageURL, err = a.app.ImageStorage().UploadImage(ctx, imageBytes)
		if err != nil {
			infra.LoggerFrom(ctx).Error("function result", "fn", "AddEntry", "error", err.Error())
			return "", err
		}
	}
	entryUUID, err := agent.AddEntryAndEnqueue(ctx, a.app, content, source, timestamp, imageURL)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "AddEntry", "error", err.Error())
		return "", err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "AddEntry", "uuid", entryUUID)
	return entryUUID, nil
}

// RunQuery runs the query agent and returns the result.
func (a *AgentService) RunQuery(ctx context.Context, question, source string) *api.QueryResult {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunQuery", "source", source, "question_preview", utils.TruncateString(question, 80))
	result := RunQuery(ctx, a.app, question, source)
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunQuery", "error", result.Error, "iterations", result.Iterations, "tool_call_count", len(result.ToolCalls), "answer_preview", utils.TruncateString(result.Answer, 100))
	return queryResultToAPI(result)
}

// ProcessLogSequential processes a single log entry through the Project Loom waterfall pipeline.
func (a *AgentService) ProcessLogSequential(ctx context.Context, logUUID, logContent, timestamp, source string) (*agent.ProcessEntryReport, error) {
	attrs := []any{"fn", "ProcessLogSequential", "uuid", logUUID, "source", source, "content_length", len(logContent)}
	if corr := infra.CorrelationFromContext(ctx); corr != nil {
		if corr.TaskID != "" {
			attrs = append(attrs, "task_id", corr.TaskID)
		}
		if corr.ParentTraceID != "" {
			attrs = append(attrs, "parent_trace_id", corr.ParentTraceID)
		}
	}
	infra.LoggerFrom(ctx).Info("function call", attrs...)
	report, err := agent.ProcessLogSequential(ctx, a.app, logUUID, logContent, timestamp, source)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "ProcessLogSequential", "uuid", logUUID, "error", err.Error())
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "ProcessLogSequential", "uuid", logUUID)
	return report, nil
}
