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

// RollupEnv provides rollup dependencies (entries with analysis, weekly summaries, semantic memory write).
type RollupEnv interface {
	// GetEntriesWithAnalysisForRollup returns formatted analyses text and source entry IDs for the date range.
	GetEntriesWithAnalysisForRollup(ctx context.Context, start, end string, limit int) (analysesText string, sourceIDs []string, err error)
	// GetWeeklySummariesForRollup returns concatenated weekly summary content and aggregated source IDs for the date range.
	GetWeeklySummariesForRollup(ctx context.Context, startDate, endDate string, limit int) (contentText string, sourceIDs []string, err error)
	// UpsertSemanticMemory creates or updates a semantic memory node (same as SpecialistsEnv).
	UpsertSemanticMemory(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks, journalEntryIDs []string) (string, error)
}

// DreamerEnv provides dreamer (nightly consolidation) dependencies. Caller typically implements SpecialistsEnv too for shared helpers.
type DreamerEnv interface {
	// LoadDreamerInputs loads the last 24h of entries and builds journal context, entry UUIDs, and recent queries text.
	LoadDreamerInputs(ctx context.Context) (*DreamerInputs, error)
	// GenerateEmbedding returns the embedding vector for text; optional taskType (e.g. for retrieval documents).
	GenerateEmbedding(ctx context.Context, text string, taskType ...string) ([]float32, error)
	// EnsureContextExists finds or creates a context by name; returns context UUID.
	EnsureContextExists(ctx context.Context, name string) (string, error)
	// TouchContextBatch appends entry UUIDs to the context and updates last_touched.
	TouchContextBatch(ctx context.Context, contextUUID string, entryUUIDs []string, relevanceBoost float64) error
	// GetContextMetadata returns metadata for synthesis decisions (lazy loading). Nil if not found.
	GetContextMetadata(ctx context.Context, contextUUID string) (*ContextMetadata, error)
	// TouchContext updates last_touched for the context (e.g. when skipping synthesis).
	TouchContext(ctx context.Context, contextUUID string, relevanceBoost float64) error
	// SynthesizeContext runs the LLM to produce a briefing and overwrites the context node content.
	SynthesizeContext(ctx context.Context, contextUUID string) error
	// RunGapDetection compares journal to knowledge and appends gaps to pending questions.
	RunGapDetection(ctx context.Context, journalContext string, entryUUIDs []string) error
	// RunProfileSynthesis merges persona facts into the user_profile context node.
	RunProfileSynthesis(ctx context.Context, personaFacts []string) error
	// RunEvolutionSynthesis runs the Cognitive Engineer and writes to system_evolution context.
	RunEvolutionSynthesis(ctx context.Context, journalContext string) error
	// UpsertSemanticMemory creates or updates a semantic memory node (same as SpecialistsEnv).
	UpsertSemanticMemory(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks, journalEntryIDs []string) (string, error)
}

// DreamerInputs holds loaded data for a dream run.
type DreamerInputs struct {
	JournalContext    string
	EntryUUIDs        []string
	RecentQueriesText string
}

// ContextMetadata is a minimal view of context node metadata for synthesis decisions (lazy loading).
type ContextMetadata struct {
	LastSynthesizedAt           string
	SourceEntryCountAtSynthesis  int
	SourceEntries               []string
	Relevance                    float64
}
