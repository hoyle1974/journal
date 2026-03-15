package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/sms"
	"github.com/jackstrohm/jot/pkg/system"
)

// SystemService provides _system collection operations (locks, dream state, debounce, onboarding) for HTTP handlers.
// Implemented by internal/service.SystemService wrapping pkg/system.
type SystemService interface {
	// Sync
	AcquireSyncLock(ctx context.Context) (bool, error)
	ReleaseSyncLock(ctx context.Context)
	GetSyncStateLastBlockHash(ctx context.Context) (hash string, exists bool, err error)
	SetSyncStateAfterProcess(ctx context.Context, blockHash string) error
	GetDebounceState(ctx context.Context) (taskName string, err error)
	SetDebounceState(ctx context.Context, taskName string, scheduledTime time.Time) error
	// Latest dream
	GetLatestDream(ctx context.Context) (*system.LatestDream, error)
	MarkLatestDreamRead(ctx context.Context) error
	WriteLatestDream(ctx context.Context, narrative, timestamp string, unread bool) error
	// Dream run
	GetDreamRunState(ctx context.Context) (*system.DreamRunState, error)
	TryAcquireDreamRunLock(ctx context.Context, runID string) (acquired bool, existingRunID string, err error)
	UpdateDreamRunPhase(ctx context.Context, runID, phase, logLine string) error
	SetDreamRunCompleted(ctx context.Context, runID string, result map[string]interface{}) error
	SetDreamRunFailed(ctx context.Context, runID string, errMsg string) error
	AppendDreamRunLog(ctx context.Context, runID string, logLine string) error
	// Onboarding
	OnboardingDocExists(ctx context.Context) (bool, error)
	SetOnboardingComplete(ctx context.Context, statusVal, seededAt string, version int) error
}

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

// QueryResult is the API response shape for a query (avoids api importing internal/agent).
type QueryResult struct {
	Answer           string                   `json:"answer"`
	Iterations       int                      `json:"iterations"`
	ToolCalls        []map[string]interface{} `json:"tool_calls,omitempty"`
	ForcedConclusion bool                     `json:"forced_conclusion,omitempty"`
	Error            bool                     `json:"error"`
	DebugLogs        []string                 `json:"debug_logs,omitempty"`
}

// DreamerResult is the API response shape for a dream run (avoids api importing internal/agent).
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
	ParseTwilioWebhook(r *http.Request) (*sms.TwilioWebhookRequest, error)
	IsAllowedPhoneNumber(phone string) bool
	ProcessIncomingSMS(ctx context.Context, app *infra.App, msg *sms.TwilioWebhookRequest) string
	SendSMS(ctx context.Context, to, body string) error
}