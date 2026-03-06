package api

import (
	"context"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
)

// PulseResult is the outcome of a pulse audit run (returned by Backend.RunPulseAudit).
type PulseResult struct {
	StaleNodes []string
	Signals    int
}

// PendingQuestion is an unresolved question (returned by Backend.GetUnresolvedPendingQuestions).
type PendingQuestion struct {
	UUID           string   `json:"uuid"`
	Question       string   `json:"question"`
	Kind           string   `json:"kind"`
	Context        string   `json:"context,omitempty"`
	SourceEntryIDs []string `json:"source_entry_ids,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

// Backend provides domain operations for HTTP handlers. Implemented by the root jot package
// so that api does not import jot (avoids circular dependency).
type Backend interface {
	AddEntry(ctx context.Context, content, source string, timestamp *string) (string, error)
	RunQuery(ctx context.Context, question, source string) *agent.QueryResult
	CreateAndSavePlan(ctx context.Context, goal string) (string, error)
	ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) (*infra.LatencyBreakdown, error)
	SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error)
	InitializePermanentContexts(ctx context.Context) error
	DecayContexts(ctx context.Context) (int, error)
	BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error)
	GetEntry(ctx context.Context, uuid string) (*journal.Entry, error)
	GetEntries(ctx context.Context, limit int) ([]journal.Entry, error)
	UpdateEntry(ctx context.Context, uuid, content string) error
	DeleteEntries(ctx context.Context, uuids []string) error
	RunDreamer(ctx context.Context) (*agent.DreamerResult, error)
	RunPulseAudit(ctx context.Context) (*PulseResult, error)
	RunJanitor(ctx context.Context) (int, error)
	GetUnresolvedPendingQuestions(ctx context.Context, limit int) ([]PendingQuestion, error)
	RunWeeklyRollup(ctx context.Context) (int, error)
	RunMonthlyRollup(ctx context.Context) (int, error)
	ResolvePendingQuestion(ctx context.Context, id, answer string) error
	GetDraftTools(ctx context.Context) ([]memory.KnowledgeNode, error)
	MarkToolDraftApplied(ctx context.Context, uuid string) error
	ValidateTwilioSignature(r *http.Request, webhookURL string) bool
	ParseTwilioWebhook(r *http.Request) (*infra.TwilioWebhookRequest, error)
	IsAllowedPhoneNumber(phone string) bool
	ProcessIncomingSMS(ctx context.Context, msg *infra.TwilioWebhookRequest) string
	SendSMS(ctx context.Context, to, body string) error
	GetFirestoreClient(ctx context.Context) (*firestore.Client, error)
	SubmitAsync(ctx context.Context, task func())
	SystemCollection() string
	WithSyncInProgress(ctx context.Context) context.Context
	IsLLMQuotaOrBillingError(err error) bool
	IsLLMPermissionOrBillingDenied(err error) bool
}
