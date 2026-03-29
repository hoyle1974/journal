package api

import (
	"context"
	"net/http"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/telegram"
)

// SystemService provides _system collection operations for HTTP handlers.
// Implemented by internal/service.SystemService wrapping pkg/system.
type SystemService interface {
	OnboardingDocExists(ctx context.Context) (bool, error)
	SetOnboardingComplete(ctx context.Context, statusVal, seededAt string, version int) error
}

// PendingQuestion is an unresolved question (returned by MemoryService.GetUnresolvedPendingQuestions).
// Intentionally omits internal fields (Answer, ResolvedAt, AskCount, etc.) from memory.PendingQuestion.
type PendingQuestion struct {
	UUID           string   `json:"uuid"`
	Question       string   `json:"question"`
	Kind           string   `json:"kind"`
	Context        string   `json:"context,omitempty"`
	SourceEntryIDs []string `json:"source_entry_uuids,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

// Entry is the API shape for a journal entry.
// Intentionally omits internal fields (ParsedImageDescription, AudioURL, Transcription) from memory.Entry.
type Entry struct {
	UUID      string `json:"uuid"`
	Content   string `json:"content"`
	Source    string `json:"source"`
	Timestamp string `json:"timestamp"`
	ImageURL  string `json:"image_url,omitempty"`
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
	GetUnresolvedPendingQuestions(ctx context.Context, limit int) ([]PendingQuestion, error)
	GetPendingQuestion(ctx context.Context, uuid string) (*PendingQuestion, error)
	ResolvePendingQuestion(ctx context.Context, id, answer string) error
}

// AgentService provides agent and cron operations for HTTP handlers.
type AgentService interface {
	AddEntry(ctx context.Context, content, source string, timestamp *string, imageBytes []byte) (string, error)
	RunQuery(ctx context.Context, question, source string) *agent.QueryResult
	ProcessLogSequential(ctx context.Context, logUUID, logContent, timestamp, source string) (*agent.ProcessEntryReport, error)
	ProcessAndRespond(ctx context.Context, input, source string) *agent.QueryResult
	RunDreamer(ctx context.Context, force bool) (*agent.DreamResult, error)
	RunMorningBriefing(ctx context.Context, force bool) (*agent.MorningBriefingResult, error)
	IngestGapAnswer(ctx context.Context, question, answer string)
}

// TelegramService provides Telegram Bot API operations for HTTP handlers.
type TelegramService interface {
	ValidateSecretToken(r *http.Request) bool
	ParseWebhook(r *http.Request) (*telegram.WebhookUpdate, *telegram.IncomingMessage, error)
	IsAllowedUser(userID int64) bool
	DownloadFile(ctx context.Context, fileID string) ([]byte, error)
	DownloadFileWithMIME(ctx context.Context, fileID string) ([]byte, string, error)
	ProcessIncomingTelegram(ctx context.Context, app *infra.App, msg *telegram.IncomingMessage) string
	SendMessage(ctx context.Context, chatID int64, body string) error
	SendPhoto(ctx context.Context, chatID int64, caption string, imageBytes []byte, mimeType string) error
}
