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

// PlannerEnv provides plan creation dependencies (knowledge graph writes).
type PlannerEnv interface {
	// UpsertKnowledge creates or updates a knowledge node. journalEntryIDs can be nil. Returns node UUID.
	UpsertKnowledge(ctx context.Context, content, nodeType, metadata string, journalEntryIDs []string) (string, error)
}

// ActiveContextItem is one context node for the system prompt (name, relevance, content).
type ActiveContextItem struct {
	ContextName string
	Relevance   float64
	Content     string
}

// PrompterEnv provides data for building the system prompt (active contexts, signals).
type PrompterEnv interface {
	// GetActiveContexts returns up to limit active context nodes with metadata.
	GetActiveContexts(ctx context.Context, limit int) ([]ActiveContextItem, error)
	// GetActiveSignals returns recent proactive signals (e.g. selfmodel thoughts) as formatted text.
	GetActiveSignals(ctx context.Context, limit int) (string, error)
}

// SpecialistsEnv provides evaluator/specialist dependencies (context lookup, semantic memory).
type SpecialistsEnv interface {
	// FindContextContent returns the content of the named context (e.g. "user_profile"), or empty if not found.
	FindContextContent(ctx context.Context, name string) (string, error)
	// UpsertSemanticMemory creates or updates a semantic memory node. entityLinks and journalEntryIDs can be nil.
	UpsertSemanticMemory(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks, journalEntryIDs []string) (string, error)
}
