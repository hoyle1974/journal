package service

import (
	"context"
	"fmt"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
)

// ConfigGetter returns the current config (allows tests to override).
type ConfigGetter func() *config.Config

// APIBackend implements api.Backend by delegating to service, journal, memory, and infra.
type APIBackend struct {
	getConfig ConfigGetter
}

// NewAPIBackend returns an api.Backend implementation for use with api.NewServer.
// getConfig is called for config-dependent operations (e.g. Twilio); pass a func that returns the active config (e.g. for tests, a getter that returns test override).
func NewAPIBackend(getConfig ConfigGetter) *APIBackend {
	return &APIBackend{getConfig: getConfig}
}

func (b *APIBackend) cfg() *config.Config {
	if b.getConfig != nil {
		return b.getConfig()
	}
	return nil
}

func (b *APIBackend) AddEntry(ctx context.Context, content, source string, timestamp *string) (string, error) {
	return (ServiceEnv{}).AddEntryAndEnqueue(ctx, content, source, timestamp)
}

func (b *APIBackend) RunQuery(ctx context.Context, question, source string) *agent.QueryResult {
	return RunQuery(ctx, question, source)
}

func (b *APIBackend) CreateAndSavePlan(ctx context.Context, goal string) (string, error) {
	return CreateAndSavePlan(ctx, goal)
}

func (b *APIBackend) ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) error {
	return ProcessEntry(ctx, entryUUID, content, timestamp, source)
}

func (b *APIBackend) SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	return journal.SaveQuery(ctx, question, answer, source, isGap)
}

func (b *APIBackend) InitializePermanentContexts(ctx context.Context) error {
	return memory.InitializePermanentContexts(ctx)
}

func (b *APIBackend) DecayContexts(ctx context.Context) (int, error) {
	return memory.DecayContexts(ctx)
}

func (b *APIBackend) BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error) {
	return journal.BackfillEntryEmbeddings(ctx, limit)
}

func (b *APIBackend) GetEntry(ctx context.Context, uuid string) (*journal.Entry, error) {
	return journal.GetEntry(ctx, uuid)
}

func (b *APIBackend) GetEntries(ctx context.Context, limit int) ([]journal.Entry, error) {
	return journal.GetEntries(ctx, limit)
}

func (b *APIBackend) UpdateEntry(ctx context.Context, uuid, content string) error {
	return journal.UpdateEntry(ctx, uuid, content)
}

func (b *APIBackend) DeleteEntries(ctx context.Context, uuids []string) error {
	return journal.DeleteEntries(ctx, uuids)
}

func (b *APIBackend) RunDreamer(ctx context.Context) (*agent.DreamerResult, error) {
	return RunDreamer(ctx)
}

func (b *APIBackend) RunPulseAudit(ctx context.Context) (*api.PulseResult, error) {
	r, err := RunPulseAudit(ctx)
	if err != nil || r == nil {
		return nil, err
	}
	return &api.PulseResult{StaleNodes: r.StaleNodes, Signals: r.Signals}, nil
}

func (b *APIBackend) RunJanitor(ctx context.Context) (int, error) {
	return RunJanitor(ctx)
}

func (b *APIBackend) GetUnresolvedPendingQuestions(ctx context.Context, limit int) ([]api.PendingQuestion, error) {
	qs, err := memory.GetUnresolvedPendingQuestions(ctx, limit)
	if err != nil {
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
	return out, nil
}

func (b *APIBackend) RunWeeklyRollup(ctx context.Context) (int, error) {
	return RunWeeklyRollup(ctx)
}

func (b *APIBackend) RunMonthlyRollup(ctx context.Context) (int, error) {
	return RunMonthlyRollup(ctx)
}

func (b *APIBackend) GetDraftTools(ctx context.Context) ([]memory.KnowledgeNode, error) {
	return memory.GetDraftTools(ctx)
}

func (b *APIBackend) MarkToolDraftApplied(ctx context.Context, uuid string) error {
	return memory.MarkToolDraftApplied(ctx, uuid)
}

func (b *APIBackend) ResolvePendingQuestion(ctx context.Context, id, answer string) error {
	q, err := memory.GetPendingQuestion(ctx, id)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("could not fetch pending question for resolution side-effects", "error", err)
	}

	if err := memory.ResolvePendingQuestion(ctx, id, answer); err != nil {
		return err
	}

	if q != nil && q.Kind == "tool_request" {
		app := infra.GetApp(ctx)
		if app != nil {
			b.SubmitAsync(ctx, func() {
				bgCtx := infra.WithApp(context.Background(), app)
				code, genErr := agent.GenerateToolCode(bgCtx, ServiceEnv{}, q.Question, answer)
				if genErr != nil {
					infra.LoggerFrom(bgCtx).Error("tool code generation failed", "error", genErr)
					return
				}
				if code == "REJECTED" {
					infra.LoggerFrom(bgCtx).Info("tool generation rejected by user")
					return
				}
				meta := fmt.Sprintf(`{"status":"draft", "tool_request_id":"%s"}`, id)
				title := fmt.Sprintf("Drafted Code for Tool Request: %s\n\n```go\n%s\n```", q.Question, code)
				_, _ = memory.UpsertKnowledge(bgCtx, title, "tool_code", meta, nil)
				infra.LoggerFrom(bgCtx).Info("tool code generated and saved to knowledge graph")
			})
		}
	}

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
	if app := infra.GetApp(ctx); app != nil {
		app.SubmitAsync(task)
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
