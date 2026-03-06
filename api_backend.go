package jot

import (
	"context"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
)

// jotBackend implements api.Backend by delegating to jot.
type jotBackend struct{}

// JotBackend is the Backend implementation for use with api.NewServer.
var JotBackend api.Backend = jotBackend{}

func (jotBackend) AddEntry(ctx context.Context, content, source string, timestamp *string) (string, error) {
	return AddEntry(ctx, content, source, timestamp)
}

func (jotBackend) RunQuery(ctx context.Context, question, source string) *agent.QueryResult {
	return RunQuery(ctx, question, source)
}

func (jotBackend) CreateAndSavePlan(ctx context.Context, goal string) (string, error) {
	return CreateAndSavePlan(ctx, goal)
}

func (jotBackend) ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) error {
	return ProcessEntry(ctx, entryUUID, content, timestamp, source)
}

func (jotBackend) SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	return SaveQuery(ctx, question, answer, source, isGap)
}

func (jotBackend) InitializePermanentContexts(ctx context.Context) error {
	return memory.InitializePermanentContexts(ctx)
}

func (jotBackend) DecayContexts(ctx context.Context) (int, error) {
	return memory.DecayContexts(ctx)
}

func (jotBackend) BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error) {
	return BackfillEntryEmbeddings(ctx, limit)
}

func (jotBackend) GetEntry(ctx context.Context, uuid string) (*journal.Entry, error) {
	return GetEntry(ctx, uuid)
}

func (jotBackend) GetEntries(ctx context.Context, limit int) ([]journal.Entry, error) {
	entries, err := GetEntries(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]journal.Entry, len(entries))
	for i := range entries {
		out[i] = entries[i]
	}
	return out, nil
}

func (jotBackend) UpdateEntry(ctx context.Context, uuid, content string) error {
	return UpdateEntry(ctx, uuid, content)
}

func (jotBackend) DeleteEntries(ctx context.Context, uuids []string) error {
	return DeleteEntries(ctx, uuids)
}

func (jotBackend) RunDreamer(ctx context.Context) (*agent.DreamerResult, error) {
	return RunDreamer(ctx)
}

func (jotBackend) RunPulseAudit(ctx context.Context) (*api.PulseResult, error) {
	r, err := RunPulseAudit(ctx)
	if err != nil || r == nil {
		return nil, err
	}
	return &api.PulseResult{StaleNodes: r.StaleNodes, Signals: r.Signals}, nil
}

func (jotBackend) RunJanitor(ctx context.Context) (int, error) {
	return RunJanitor(ctx)
}

func (jotBackend) GetUnresolvedPendingQuestions(ctx context.Context, limit int) ([]api.PendingQuestion, error) {
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

func (jotBackend) RunWeeklyRollup(ctx context.Context) (int, error) {
	return RunWeeklyRollup(ctx)
}

func (jotBackend) RunMonthlyRollup(ctx context.Context) (int, error) {
	return RunMonthlyRollup(ctx)
}

func (jotBackend) ResolvePendingQuestion(ctx context.Context, id, answer string) error {
	return memory.ResolvePendingQuestion(ctx, id, answer)
}

func (jotBackend) ValidateTwilioSignature(r *http.Request, webhookURL string) bool {
	return ValidateTwilioSignature(r, webhookURL)
}

func (jotBackend) ParseTwilioWebhook(r *http.Request) (*infra.TwilioWebhookRequest, error) {
	return ParseTwilioWebhook(r)
}

func (jotBackend) IsAllowedPhoneNumber(phone string) bool {
	return IsAllowedPhoneNumber(phone)
}

func (jotBackend) ProcessIncomingSMS(ctx context.Context, msg *infra.TwilioWebhookRequest) string {
	if msg == nil {
		return ""
	}
	return ProcessIncomingSMS(ctx, (*TwilioWebhookRequest)(msg))
}

func (jotBackend) SendSMS(ctx context.Context, to, body string) error {
	return SendSMS(ctx, to, body)
}

func (jotBackend) GetFirestoreClient(ctx context.Context) (*firestore.Client, error) {
	return GetFirestoreClient(ctx)
}

func (jotBackend) SubmitAsync(ctx context.Context, task func()) {
	SubmitAsync(ctx, task)
}

func (jotBackend) SystemCollection() string {
	return SystemCollection
}

func (jotBackend) WithSyncInProgress(ctx context.Context) context.Context {
	return WithSyncInProgress(ctx)
}

func (jotBackend) IsLLMQuotaOrBillingError(err error) bool {
	return IsLLMQuotaOrBillingError(err)
}

func (jotBackend) IsLLMPermissionOrBillingDenied(err error) bool {
	return IsLLMPermissionOrBillingDenied(err)
}
