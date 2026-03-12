package api

import (
	"context"
	"net/http"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
)

// PulseResult is the outcome of a pulse audit run (returned by AgentService.RunPulseAudit).
type PulseResult struct {
	StaleNodes []string
	Signals    int
}

// PendingQuestion is an unresolved question (returned by MemoryService.GetUnresolvedPendingQuestions).
type PendingQuestion struct {
	UUID           string   `json:"uuid"`
	Question       string   `json:"question"`
	Kind           string   `json:"kind"`
	Context        string   `json:"context,omitempty"`
	SourceEntryIDs []string `json:"source_entry_ids,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

// QueryResult is the API response shape for a query (avoids api importing pkg/agent).
type QueryResult struct {
	Answer           string                   `json:"answer"`
	Iterations       int                      `json:"iterations"`
	ToolCalls        []map[string]interface{} `json:"tool_calls,omitempty"`
	ForcedConclusion bool                     `json:"forced_conclusion,omitempty"`
	Error            bool                     `json:"error"`
	DebugLogs        []string                 `json:"debug_logs,omitempty"`
}

// DreamerResult is the API response shape for a dream run (avoids api importing pkg/agent).
type DreamerResult struct {
	EntriesProcessed    int `json:"entries_processed"`
	FactsExtracted      int `json:"facts_extracted"`
	FactsWritten        int `json:"facts_written"`
	ContextsSynthesized int `json:"contexts_synthesized,omitempty"`
}

// Entry is the API shape for a journal entry (avoids api importing pkg/journal).
type Entry struct {
	UUID      string `json:"uuid"`
	Content   string `json:"content"`
	Source    string `json:"source"`
	Timestamp string `json:"timestamp"`
}

// KnowledgeNode is the API shape for a knowledge node (avoids api importing pkg/memory).
type KnowledgeNode struct {
	UUID            string   `json:"uuid"`
	Content         string   `json:"content"`
	NodeType        string   `json:"node_type"`
	Metadata        string   `json:"metadata"`
	Timestamp       string   `json:"timestamp"`
	JournalEntryIDs []string `json:"journal_entry_ids,omitempty"`
}

// JournalService provides journal and entry operations for HTTP handlers.
type JournalService interface {
	SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error)
	GetEntry(ctx context.Context, uuid string) (*Entry, error)
	GetEntries(ctx context.Context, limit int) ([]Entry, error)
	UpdateEntry(ctx context.Context, uuid, content string) error
	DeleteEntries(ctx context.Context, uuids []string) error
	BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error)
}

// MemoryService provides memory and knowledge operations for HTTP handlers.
type MemoryService interface {
	InitializePermanentContexts(ctx context.Context) error
	DecayContexts(ctx context.Context) (int, error)
	GetUnresolvedPendingQuestions(ctx context.Context, limit int) ([]PendingQuestion, error)
	ResolvePendingQuestion(ctx context.Context, id, answer string) error
}

// AgentService provides agent, query, dreamer, and cron operations for HTTP handlers.
type AgentService interface {
	AddEntry(ctx context.Context, content, source string, timestamp *string) (string, error)
	RunQuery(ctx context.Context, question, source string) *QueryResult
	CreateAndSavePlan(ctx context.Context, goal string) (string, error)
	ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) (*infra.LatencyBreakdown, error)
	RunDreamer(ctx context.Context) (*DreamerResult, error)
	RunDreamerWithProgress(ctx context.Context, runID string, progress agent.DreamerProgress) (*DreamerResult, error)
	RunPulseAudit(ctx context.Context) (*PulseResult, error)
	RunJanitor(ctx context.Context) (int, error)
	RunWeeklyRollup(ctx context.Context) (int, error)
	RunMonthlyRollup(ctx context.Context) (int, error)
}

// SMSService provides Twilio/SMS operations for HTTP handlers.
type SMSService interface {
	ValidateTwilioSignature(r *http.Request, webhookURL string) bool
	ParseTwilioWebhook(r *http.Request) (*infra.TwilioWebhookRequest, error)
	IsAllowedPhoneNumber(phone string) bool
	ProcessIncomingSMS(ctx context.Context, app *infra.App, msg *infra.TwilioWebhookRequest) string
	SendSMS(ctx context.Context, to, body string) error
}