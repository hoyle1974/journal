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

func dreamerResultToAPI(r *agent.DreamerResult) *api.DreamerResult {
	if r == nil {
		return nil
	}
	return &api.DreamerResult{
		EntriesProcessed:    r.EntriesProcessed,
		FactsExtracted:      r.FactsExtracted,
		FactsWritten:        r.FactsWritten,
		ContextsSynthesized: r.ContextsSynthesized,
	}
}

// AgentService handles agent, query, dreamer, and cron operations for the API.
type AgentService struct {
	app *infra.App
}

// NewAgentService returns an AgentService for use with api.Server. app is the runtime app (Firestore, Gemini, etc.).
func NewAgentService(app *infra.App) *AgentService {
	return &AgentService{app: app}
}

// AddEntry adds an entry and enqueues processing.
func (a *AgentService) AddEntry(ctx context.Context, content, source string, timestamp *string) (string, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "AddEntry", "source", source, "content_length", len(content))
	ctx = infra.WithApp(ctx, a.app)
	entryUUID, err := agent.AddEntryAndEnqueue(ctx, content, source, timestamp)
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

// CreateAndSavePlan creates a plan and saves it to the knowledge graph.
func (a *AgentService) CreateAndSavePlan(ctx context.Context, goal string) (string, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "CreateAndSavePlan", "goal_preview", utils.TruncateString(goal, 80))
	plan, err := CreateAndSavePlan(ctx, a.app, goal)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "CreateAndSavePlan", "error", err.Error())
		return "", err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "CreateAndSavePlan", "plan_length", len(plan))
	return plan, nil
}

// ProcessEntry processes a single entry (embedding, analysis, etc.).
func (a *AgentService) ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) (*infra.LatencyBreakdown, error) {
	attrs := []any{"fn", "ProcessEntry", "uuid", entryUUID, "source", source, "content_length", len(content)}
	if corr := infra.CorrelationFromContext(ctx); corr != nil {
		if corr.TaskID != "" {
			attrs = append(attrs, "task_id", corr.TaskID)
		}
		if corr.ParentTraceID != "" {
			attrs = append(attrs, "parent_trace_id", corr.ParentTraceID)
		}
	}
	infra.LoggerFrom(ctx).Info("function call", attrs...)
	ctx = infra.WithApp(ctx, a.app)
	breakdown, err := agent.ProcessEntry(ctx, a.app, entryUUID, content, timestamp, source)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "ProcessEntry", "uuid", entryUUID, "error", err.Error())
		return breakdown, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "ProcessEntry", "uuid", entryUUID)
	return breakdown, nil
}

// RunDreamer runs the dreamer pipeline.
func (a *AgentService) RunDreamer(ctx context.Context) (*api.DreamerResult, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunDreamer")
	result, err := agent.RunDreamer(ctx, a.app, nil)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "RunDreamer", "error", err.Error())
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunDreamer", "entries_processed", result.EntriesProcessed, "facts_extracted", result.FactsExtracted, "facts_written", result.FactsWritten)
	return dreamerResultToAPI(result), nil
}

// RunDreamerWithProgress runs the dreamer pipeline with progress callbacks (e.g. for async run state in Firestore).
func (a *AgentService) RunDreamerWithProgress(ctx context.Context, runID string, progress agent.DreamerProgress) (*api.DreamerResult, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunDreamerWithProgress", "dream_run_id", runID)
	result, err := agent.RunDreamer(ctx, a.app, &agent.RunDreamerOpts{RunID: runID, Progress: progress})
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "RunDreamerWithProgress", "error", err.Error())
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunDreamerWithProgress", "entries_processed", result.EntriesProcessed)
	return dreamerResultToAPI(result), nil
}

// RunPulseAudit runs the pulse audit and returns the API-shaped result.
func (a *AgentService) RunPulseAudit(ctx context.Context) (*api.PulseResult, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunPulseAudit")
	r, err := RunPulseAudit(ctx)
	if err != nil || r == nil {
		if err != nil {
			infra.LoggerFrom(ctx).Warn("function result", "fn", "RunPulseAudit", "error", err.Error())
		} else {
			infra.LoggerFrom(ctx).Warn("function result", "fn", "RunPulseAudit", "result", "nil")
		}
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunPulseAudit", "signals", r.Signals, "stale_nodes", len(r.StaleNodes))
	return &api.PulseResult{StaleNodes: r.StaleNodes, Signals: r.Signals}, nil
}

// RunJanitor runs the janitor (garbage collection).
func (a *AgentService) RunJanitor(ctx context.Context) (int, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunJanitor")
	deleted, err := RunJanitor(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "RunJanitor", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunJanitor", "deleted", deleted)
	return deleted, nil
}

// RunWeeklyRollup runs the weekly rollup.
func (a *AgentService) RunWeeklyRollup(ctx context.Context) (int, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunWeeklyRollup")
	ctx = infra.WithApp(ctx, a.app)
	n, err := RunWeeklyRollup(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "RunWeeklyRollup", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunWeeklyRollup", "weekly_entries", n)
	return n, nil
}

// RunMonthlyRollup runs the monthly rollup.
func (a *AgentService) RunMonthlyRollup(ctx context.Context) (int, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunMonthlyRollup")
	ctx = infra.WithApp(ctx, a.app)
	n, err := RunMonthlyRollup(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "RunMonthlyRollup", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunMonthlyRollup", "monthly_weekly_nodes", n)
	return n, nil
}
