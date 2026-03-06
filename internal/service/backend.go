package service

import (
	"context"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

// ConfigGetter returns the current config (allows tests to override).
type ConfigGetter func() *config.Config

// APIBackend implements api.Backend by delegating to service, journal, memory, and infra.
// App is passed explicitly so handlers do not rely on context for dependencies.
type APIBackend struct {
	getConfig ConfigGetter
	app      *infra.App
}

// NewAPIBackend returns an api.Backend implementation for use with api.NewServer.
// getConfig is called for config-dependent operations (e.g. Twilio). app is the runtime app (Firestore, Gemini, pools).
func NewAPIBackend(getConfig ConfigGetter, app *infra.App) *APIBackend {
	return &APIBackend{getConfig: getConfig, app: app}
}

func (b *APIBackend) cfg() *config.Config {
	if b.getConfig != nil {
		return b.getConfig()
	}
	return nil
}

func (b *APIBackend) AddEntry(ctx context.Context, content, source string, timestamp *string) (string, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "AddEntry", "source", source, "content_length", len(content))
	ctx = infra.WithApp(ctx, b.app)
	entryUUID, err := agent.AddEntryAndEnqueue(ctx, content, source, timestamp)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "AddEntry", "error", err.Error())
		return "", err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "AddEntry", "uuid", entryUUID)
	return entryUUID, nil
}

func (b *APIBackend) RunQuery(ctx context.Context, question, source string) *agent.QueryResult {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunQuery", "source", source, "question_preview", utils.TruncateString(question, 80))
	result := RunQuery(ctx, b.app, question, source)
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunQuery", "error", result.Error, "iterations", result.Iterations, "tool_call_count", len(result.ToolCalls), "answer_preview", utils.TruncateString(result.Answer, 100))
	return result
}

func (b *APIBackend) CreateAndSavePlan(ctx context.Context, goal string) (string, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "CreateAndSavePlan", "goal_preview", utils.TruncateString(goal, 80))
	plan, err := CreateAndSavePlan(ctx, b.app, goal) // CreateAndSavePlan sets app on ctx
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "CreateAndSavePlan", "error", err.Error())
		return "", err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "CreateAndSavePlan", "plan_length", len(plan))
	return plan, nil
}

func (b *APIBackend) ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) (*infra.LatencyBreakdown, error) {
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
	breakdown, err := agent.ProcessEntry(ctx, b.app, entryUUID, content, timestamp, source)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "ProcessEntry", "uuid", entryUUID, "error", err.Error())
		return breakdown, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "ProcessEntry", "uuid", entryUUID)
	return breakdown, nil
}

func (b *APIBackend) SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "SaveQuery", "source", source, "is_gap", isGap, "question_preview", utils.TruncateString(question, 60))
	id, err := journal.SaveQuery(ctx, question, answer, source, isGap)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "SaveQuery", "error", err.Error())
		return "", err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "SaveQuery", "id", id)
	return id, nil
}

func (b *APIBackend) InitializePermanentContexts(ctx context.Context) error {
	return memory.InitializePermanentContexts(ctx)
}

func (b *APIBackend) DecayContexts(ctx context.Context) (int, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "DecayContexts")
	count, err := memory.DecayContexts(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "DecayContexts", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "DecayContexts", "decayed_count", count)
	return count, nil
}

func (b *APIBackend) BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "BackfillEntryEmbeddings", "limit", limit)
	processed, err := journal.BackfillEntryEmbeddings(ctx, limit)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "BackfillEntryEmbeddings", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "BackfillEntryEmbeddings", "processed", processed)
	return processed, nil
}

func (b *APIBackend) GetEntry(ctx context.Context, uuid string) (*journal.Entry, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "GetEntry", "uuid", uuid)
	entry, err := journal.GetEntry(ctx, uuid)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("function result", "fn", "GetEntry", "uuid", uuid, "error", err.Error())
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "GetEntry", "uuid", uuid, "found", true)
	return entry, nil
}

func (b *APIBackend) GetEntries(ctx context.Context, limit int) ([]journal.Entry, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "GetEntries", "limit", limit)
	entries, err := journal.GetEntries(ctx, limit)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "GetEntries", "error", err.Error())
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "GetEntries", "count", len(entries))
	return entries, nil
}

func (b *APIBackend) UpdateEntry(ctx context.Context, uuid, content string) error {
	infra.LoggerFrom(ctx).Info("function call", "fn", "UpdateEntry", "uuid", uuid, "content_length", len(content))
	err := journal.UpdateEntry(ctx, uuid, content)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "UpdateEntry", "uuid", uuid, "error", err.Error())
		return err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "UpdateEntry", "uuid", uuid)
	return nil
}

func (b *APIBackend) DeleteEntries(ctx context.Context, uuids []string) error {
	infra.LoggerFrom(ctx).Info("function call", "fn", "DeleteEntries", "uuid_count", len(uuids))
	err := journal.DeleteEntries(ctx, uuids)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "DeleteEntries", "error", err.Error())
		return err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "DeleteEntries", "deleted", len(uuids))
	return nil
}

func (b *APIBackend) RunDreamer(ctx context.Context) (*agent.DreamerResult, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunDreamer")
	result, err := RunDreamer(ctx, b.app)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "RunDreamer", "error", err.Error())
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunDreamer", "entries_processed", result.EntriesProcessed, "facts_extracted", result.FactsExtracted, "facts_written", result.FactsWritten)
	return result, nil
}

func (b *APIBackend) RunPulseAudit(ctx context.Context) (*api.PulseResult, error) {
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

func (b *APIBackend) RunJanitor(ctx context.Context) (int, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunJanitor")
	deleted, err := RunJanitor(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "RunJanitor", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunJanitor", "deleted", deleted)
	return deleted, nil
}

func (b *APIBackend) GetUnresolvedPendingQuestions(ctx context.Context, limit int) ([]api.PendingQuestion, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "GetUnresolvedPendingQuestions", "limit", limit)
	qs, err := memory.GetUnresolvedPendingQuestions(ctx, limit)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "GetUnresolvedPendingQuestions", "error", err.Error())
		return nil, err
	}
	out := make([]api.PendingQuestion, len(qs))
	for i := range qs {
		out[i] = api.PendingQuestion{
			UUID:           qs[i].UUID,
			Question:       qs[i].Question,
			Kind:           qs[i].Kind,
			Context:        qs[i].Context,
			SourceEntryIDs: qs[i].SourceEntryIDs,
			CreatedAt:      qs[i].CreatedAt,
		}
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "GetUnresolvedPendingQuestions", "count", len(out))
	return out, nil
}

func (b *APIBackend) RunWeeklyRollup(ctx context.Context) (int, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunWeeklyRollup")
	ctx = infra.WithApp(ctx, b.app)
	n, err := RunWeeklyRollup(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "RunWeeklyRollup", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunWeeklyRollup", "weekly_entries", n)
	return n, nil
}

func (b *APIBackend) RunMonthlyRollup(ctx context.Context) (int, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "RunMonthlyRollup")
	ctx = infra.WithApp(ctx, b.app)
	n, err := RunMonthlyRollup(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "RunMonthlyRollup", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "RunMonthlyRollup", "monthly_weekly_nodes", n)
	return n, nil
}

func (b *APIBackend) ResolvePendingQuestion(ctx context.Context, id, answer string) error {
	infra.LoggerFrom(ctx).Info("function call", "fn", "ResolvePendingQuestion", "id", id, "answer_length", len(answer))
	if err := memory.ResolvePendingQuestion(ctx, id, answer); err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "ResolvePendingQuestion", "id", id, "error", err.Error())
		return err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "ResolvePendingQuestion", "id", id)
	return nil
}

func (b *APIBackend) ValidateTwilioSignature(r *http.Request, webhookURL string) bool {
	return infra.ValidateTwilioSignature(b.cfg(), r, webhookURL)
}

func (b *APIBackend) ParseTwilioWebhook(r *http.Request) (*infra.TwilioWebhookRequest, error) {
	return infra.ParseTwilioWebhook(r)
}

func (b *APIBackend) IsAllowedPhoneNumber(phone string) bool {
	return infra.IsAllowedPhoneNumber(b.cfg(), phone)
}

func (b *APIBackend) ProcessIncomingSMS(ctx context.Context, msg *infra.TwilioWebhookRequest) string {
	if msg == nil {
		return ""
	}
	return ProcessIncomingSMS(ctx, msg)
}

func (b *APIBackend) SendSMS(ctx context.Context, to, body string) error {
	return infra.SendSMS(ctx, b.cfg(), to, body)
}

func (b *APIBackend) GetFirestoreClient(ctx context.Context) (*firestore.Client, error) {
	return infra.GetFirestoreClient(ctx)
}

func (b *APIBackend) SubmitAsync(ctx context.Context, task func()) {
	if b.app != nil {
		b.app.SubmitAsync(task)
	}
}

func (b *APIBackend) SystemCollection() string {
	return infra.SystemCollection
}

func (b *APIBackend) WithSyncInProgress(ctx context.Context) context.Context {
	return infra.WithSyncInProgress(ctx)
}

func (b *APIBackend) IsLLMQuotaOrBillingError(err error) bool {
	return infra.IsLLMQuotaOrBillingError(err)
}

func (b *APIBackend) IsLLMPermissionOrBillingDenied(err error) bool {
	return infra.IsLLMPermissionOrBillingDenied(err)
}
