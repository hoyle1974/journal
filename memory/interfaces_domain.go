package memory

import "context"

// UpsertOptions consolidates optional parameters for knowledge ingestion.
// If Embedding is nil the library generates it automatically.
type UpsertOptions struct {
	Embedding       []float32
	EntityLinks     []string
	JournalEntryIDs []string
	SPO             *SPOExtra
}

// SearchOptions consolidates parameters for graph and vector queries.
// Limit defaults to 20 when <= 0. MinSignificance defaults to 0.0.
type SearchOptions struct {
	Limit           int
	MinSignificance float64
}

// EntryStore manages episodic log entries.
// GetEntry and UpdateEntry keep their original names to avoid Go method-name
// collisions with TaskStore.GetTask/UpdateTask on the concrete *Store type.
type EntryStore interface {
	AddEntry(ctx context.Context, content, source string, timestamp *string, imageURL string) (string, error)
	GetEntry(ctx context.Context, uuid string) (*Entry, error)
	List(ctx context.Context, limit int, ascending bool) ([]Entry, error)
	Search(ctx context.Context, keywords string, limit int) ([]Entry, error)
	UpdateEntry(ctx context.Context, uuid, newContent string) error
	Delete(ctx context.Context, uuids []string) error
}

// KnowledgeStore manages the semantic knowledge graph.
type KnowledgeStore interface {
	Upsert(ctx context.Context, content, nodeType, domain string, weight float64, opts UpsertOptions) (string, error)
	GetByID(ctx context.Context, id string) (*KnowledgeNodeWithLinks, error)
	GetByIDs(ctx context.Context, ids []string) ([]KnowledgeNodeWithLinks, error)
	AddEntityLink(ctx context.Context, sourceUUID, targetUUID string) error
	UpdateProjectStatus(ctx context.Context, nodeID, status string) error
}

// GraphStore provides graph traversal and semantic search.
type GraphStore interface {
	Expand(ctx context.Context, seedID string, queryVector []float32, hops, limitPerEdge int) (*SubGraph, error)
	ExpandMulti(ctx context.Context, seedIDs []string, queryVector []float32, hops, limitPerEdge int) (*SubGraph, error)
	QuerySimilar(ctx context.Context, queryVector []float32, opts SearchOptions) ([]KnowledgeNode, error)
	SearchKeywords(ctx context.Context, keywords string, limit int) ([]KnowledgeNode, error)
	Rerank(ctx context.Context, query string, nodes []KnowledgeNode, topN int) ([]KnowledgeNode, error)
}

// TaskStore manages tasks and subtask decomposition.
// Method names match existing Store methods exactly to avoid collisions with
// EntryStore (GetEntry vs GetTask).
type TaskStore interface {
	CreateTask(ctx context.Context, t *Task) (string, error)
	GetTask(ctx context.Context, uuid string) (*Task, error)
	UpdateTask(ctx context.Context, uuid string, opts *UpdateTaskOpts) error
	UpdateTaskStatus(ctx context.Context, uuid, newStatus, reflectionReason string) error
	BrainstormSubtasks(ctx context.Context, parentID string) (string, error)
	GetOpenRootTasks(ctx context.Context, limit int) ([]Task, error)
}

// AgentOps groups agentic and background synthesis operations.
type AgentOps interface {
	SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error)
	InsertPendingQuestions(ctx context.Context, questions []PendingQuestion) error
	GetUnresolvedQuestions(ctx context.Context, limit int) ([]PendingQuestion, error)
	ResolveQuestion(ctx context.Context, uuid, answer string) error
}

// AdminOps covers maintenance, GC, and migrations.
// Do not call these on hot paths.
type AdminOps interface {
	MigrateMetadata(ctx context.Context, dryRun bool) (int, error)
	BackfillEmbeddings(ctx context.Context, limit int) (int, error)
}
