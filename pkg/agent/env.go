package agent

import (
	"context"

	"github.com/jackstrohm/jot/pkg/journal"
)

// FOHEnv provides FOH (query agent) dependencies that may live in the main app (e.g. jot).
// This allows pkg/agent to avoid importing the root package.
type FOHEnv interface {
	// BuildSystemPrompt returns the system prompt for the query agent (date, contexts, recent history, etc.).
	BuildSystemPrompt(ctx context.Context) string
	// AddEntryAndEnqueue adds the entry to the journal and enqueues process-entry. Returns entry UUID.
	AddEntryAndEnqueue(ctx context.Context, content, source string, timestamp *string) (string, error)
	// EnqueueSaveQuery enqueues a task to save the query and answer (and whether it was a knowledge gap).
	EnqueueSaveQuery(ctx context.Context, question, answer, source string, isGap bool) error
	// GetEntry returns the entry by UUID, or nil if not found.
	GetEntry(ctx context.Context, entryUUID string) (*journal.Entry, error)
}
