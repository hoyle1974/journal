# memory Library Blueprint

## 1. Project Vision

`github.com/hoyle1974/memory` is a pure Go library that provides the **GraphRAG memory layer** for the Jot agentic second-brain system. It is not an application — there is no `main` package, no HTTP server, and no CLI. Callers (e.g. `jot`) construct a `Store` and use it to read and write structured knowledge to Firestore.

The library handles:
- Persistent, typed **knowledge nodes** backed by Firestore (episodic logs, semantic facts, tasks, queries, contexts, pending questions).
- **Vector + keyword hybrid search** with Reciprocal Rank Fusion (RRF) and optional LLM re-ranking.
- **Graph traversal** (1-hop neighbourhood expansion via SPO edges and entity links).
- **Rollup** (weekly and monthly summary nodes).
- **Task management** (create, update, query tasks with LLM-driven decomposition).
- **Context nodes** (active briefings).
- **Pending questions** (knowledge gaps and contradictions to be clarified by the user).
- **Schema validation and normalization** for all node metadata types.

## 2. Core Abstraction: `Store`

```go
type Store struct {
    db       *firestore.Client
    embedder Embedder
    llm      LLMDispatcher
    log      *slog.Logger
}

func New(db *firestore.Client, embedder Embedder, llm LLMDispatcher, opts ...Option) *Store
```

All operations are methods on `*Store`. Callers own the lifecycle of `db`, `embedder`, and `llm`.

### Interfaces

| Interface | Purpose |
|-----------|---------|
| `Embedder` | Generates float32 vector embeddings for semantic search. Two methods: `GenerateEmbedding` (single) and `GenerateEmbeddingsBatch` (batch). |
| `LLMDispatcher` | Makes single-shot LLM calls and returns the text response. One method: `Dispatch(ctx, LLMRequest) (string, error)`. |

### Options

| Option | Effect |
|--------|--------|
| `WithLogger(l *slog.Logger)` | Attaches a structured logger; otherwise logs are discarded. |

## 3. Firestore Schema

All node types live in the **single `journal` collection** (`KnowledgeCollection = "journal"`). Node type is distinguished by the `node_type` field.

| `node_type` | Struct | Purpose |
|-------------|--------|---------|
| `log` | `Entry` | Raw episodic journal entries. Must never be deleted by maintenance code. |
| `person` | `KnowledgeNode` + `PersonMeta` | People in the user's life. |
| `project` | `KnowledgeNode` + `ProjectGoalMeta` | Projects the user is working on. |
| `goal` | `KnowledgeNode` + `ProjectGoalMeta` | Goals (may be sub-goals of a project). |
| `preference` | `KnowledgeNode` + `PreferenceMeta` | User preferences (food, workflow, tech). |
| `event` | `KnowledgeNode` + `EventMilestoneMeta` | Events (celebration, work, health). |
| `milestone` | `KnowledgeNode` + `EventMilestoneMeta` | Project/life milestones. |
| `place` | `KnowledgeNode` + `PlaceMeta` | Places (home, office, travel). |
| `asset` | `KnowledgeNode` + `AssetToolMeta` | Assets (software, hardware, accounts). |
| `tool` | `KnowledgeNode` + `AssetToolMeta` | Tools (same schema as asset). |
| `generic` | `KnowledgeNode` + `GenericNodeMeta` | Uncategorized facts. |
| `identity_anchor` | `KnowledgeNode` + `IdentityMeta` | Primary user identity. Never deleted. |
| `user_identity` | `KnowledgeNode` + `UserIdentityMeta` | Self-referential identity statements. Never deleted. |
| `task` | `Task` | Task/todo items with status, priority, subtask hierarchy. |
| `query` | `QueryLog` | Logged Q&A pairs from the FOH loop. |
| `pending_question` | `PendingQuestion` | Knowledge gaps/contradictions for the user to resolve. |
| `context` | `ContextMetadata` | Active briefings with relevance decay. |
| `weekly_summary` | `KnowledgeNode` | Weekly rollup summary node. |
| `monthly_summary` | `KnowledgeNode` | Monthly rollup summary node. |

### SPO (Subject–Predicate–Object) Triples

Relational knowledge nodes use three extra fields:
- `predicate` — normalized relationship verb (e.g. `works_at`, `is_part_of`).
- `object_uuid` — UUID of the object entity node (empty when object is a raw string).

### Entity Links

`KnowledgeNodeWithLinks` extends `KnowledgeNode` with `EntityLinks []string` (UUIDs of related nodes) and `JournalEntryIDs []string` (source episodic entries). Used for graph traversal.

## 4. File Map

| File | Responsibility |
|------|---------------|
| `memory.go` | `Store` struct, `New`, `WithLogger`. |
| `interfaces.go` | `Embedder`, `LLMDispatcher`, `LLMRequest` interfaces and task-type constants. |
| `schema.go` | Node-type constants, metadata structs, validator/normalizer registry, `ValidateMetadata`, `NormalizeMetadata`, `MetadataToJSON`, `ParseSPOTriple`, `IsSPOTriple`, `NormalizedPredicate`. |
| `knowledge_crud.go` | Package constants, `KnowledgeNode`/`KnowledgeNodeWithLinks` structs, point-reads (`GetKnowledgeNodeByID`, `GetKnowledgeNodesByIDs`), entity-link helpers (`AppendJournalEntryIDsToNode`, `AddEntityLink`), `FindNearestWithThreshold`, `GetUserIdentityNodes`, `GetActiveSignals`, `ListKnowledgeNodes`. |
| `knowledge_upsert.go` | LLM-driven fact-collision detection (`evaluateFactCollision`), all `Upsert*` variants (`UpsertKnowledge`, `UpsertSemanticMemory`, `UpsertSemanticMemoryPreembedded`, `UpsertSemanticMemoryPreembeddedWithSPO`), private `upsertSemanticMemoryWithVector`. |
| `knowledge_search.go` | Vector KNN search (`QuerySimilarNodes`, `QuerySimilarSemanticNodes`), keyword scan (`SearchKnowledgeNodes`), entity/project name finders (`FindEntityNodeByName`, `FindProjectOrGoalByName`), `DiscoverRelatedNodes`. |
| `knowledge_spo.go` | SPO edge traversal: `QueryIncomingSPOEdges` (nodes whose `object_uuid` equals the target — incoming SPO edges), `QueryNodesLinkingTo` (incoming entity-link edges). |
| `knowledge_project.go` | Project/goal status mutations (`UpdateProjectStatus`), archive-summary append (`AppendToProjectArchiveSummary`), completed-project lookup (`GetLinkedCompletedProjectID`). |
| `entry_nodes.go` | CRUD for `Entry` (episodic log nodes): `AddEntry`, `GetEntries*`, `GetEntriesWithAnalysis*`, `SearchEntriesByKeyword`, etc. |
| `entry_nodes_extended.go` | Extended entry helpers: date-range queries, source filtering, pagination. |
| `context.go` | Context node operations: `CreateContext`, `GetActiveContexts`, `UpdateContext`, `SynthesizeContext`. |
| `query_nodes.go` | Query log operations: `SaveQuery`, `GetRecentQueries`, `GetQueryByID`. |
| `pending.go` | Pending question operations: `InsertPendingQuestions`, `GetPendingQuestions`, `ResolvePendingQuestion`. |
| `pending_dedup_test.go` | Tests for pending-question deduplication. |
| `task_engine.go` | Task CRUD and LLM-driven decomposition: `CreateTask`, `GetTask`, `UpdateTask`, `ListTasks`, `BrainstormSubtasks`. |
| `task_nodes.go` | Low-level task Firestore helpers. |
| `task_query.go` | Task query helpers (filter by status, parent, etc.). |
| `graph.go` | `GraphExpand`: BFS multi-hop graph traversal returning `*SubGraph` with `Nodes map[string]KnowledgeNodeWithLinks` and `Edges []Edge`. `SubGraph.ToMarkdown` serializes for LLM injection. `pruneCandidates` prunes BFS frontiers by cosine similarity. |
| `rag.go` | `HybridSearch` pipeline: vector search + keyword search + RRF fusion + optional re-rank. Log helpers for search confidence. |
| `rerank.go` | `RerankNodes`: LLM-based relevance re-ranking of a candidate node list. |
| `analysis.go` | `JournalAnalysis`, `Entity`, `OpenLoop` types; `NormalizeEntityStatus` helper. |
| `rollup.go` | `GetWeeklySummaryNodesInRange`, `GetMonthlySummaryNodesInRange`. |
| `migrate.go` | `MigrateKnowledgeMetadata`: one-off schema migration with optional dry-run. |
| `math.go` | Cosine similarity, vector utilities. |
| `text.go` | String utilities: `truncateString`, `sanitizePrompt`, `wrapAsUserData`, `parseKeyValueMap`, tag normalization. |
| `errors.go` | `wrapFirestoreIndexError`: annotates missing-index errors with actionable messages. |
| `log.go` | Logging helpers on `Store`. |
| `format_test.go` | Tests for text/format utilities. |
| `schema_test.go` | Tests for schema validation and normalization. |
| `graph_test.go` | Tests for graph expansion. |
| `entry_nodes_test.go` | Tests for entry node operations. |
| `query_nodes_test.go` | Tests for query node operations. |
| `task_nodes_test.go` | Tests for task engine. |
| `analysis_test.go` | Tests for `NormalizeEntityStatus`. |
| `prompts/` | Embedded prompt text files (`.txt`) used by context synthesis and executive summary operations. |
| `gemini/` | Gemini-specific adapter implementations (embedder, LLM dispatcher). |

## 5. Key Algorithms

### Hybrid Search (RAG pipeline)

1. **Vector search** — ANN query against Firestore vector index using the query embedding.
2. **Keyword search** — Firestore full-text or field-contains query.
3. **RRF fusion** (`FuseKnowledgeNodes`) — Reciprocal Rank Fusion combines both result lists into a single ranked set. `rrfK = 60`.
4. **Optional LLM re-rank** (`RerankNodes`) — pass top-N fused candidates to the LLM, which outputs relevance-ordered indices.

### Graph Expand

`GraphExpand(ctx, seedID, queryVector, hops, limitPerEdge)` performs a BFS traversal and returns `*SubGraph`:
- **Seed** — fetched via `GetKnowledgeNodeByID`; `EntityLinks` used for hop-0 edge collection.
- **Each hop** — for all nodes in the current frontier, fires `QueryIncomingSPOEdges` and `QueryNodesLinkingTo` concurrently via `errgroup`, and traverses the node's own `ObjectUUID` as an intrinsic outgoing SPO edge. Entity link UUIDs come from the stored node's `EntityLinks` field.
- **Cycle detection** — a `visited` map prevents re-expansion of already-seen nodes.
- **Semantic pruning** — inter-hop frontier is pruned to top-K by cosine similarity to `queryVector`. If `queryVector` is nil, a hard cap of `limitPerEdge` is applied.
- **Batch fetch** — `GetKnowledgeNodesByIDs` uses `db.GetAll` in chunks of 100.

`SubGraph` contains `Nodes map[string]KnowledgeNodeWithLinks` and `Edges []Edge`. Call `sg.ToMarkdown(seedID)` to serialize for LLM injection.

`KnowledgeNode` now includes an `Embedding []float32` field populated on all reads (extracted from Firestore `Vector32`).

## 6. Schema Registration

To add a new node type:

1. Add a `NodeType*` constant in `schema.go`.
2. Define a `*Meta` struct with JSON tags.
3. Implement `validate*` and `normalize*` functions.
4. Register in the `registry` map in `schema.go`.
5. Add the node type to the table in this blueprint.

## 7. Prompt Files

Prompt text files live in `prompts/` and are embedded at compile time using `//go:embed`. Files:

| File | Used by |
|------|---------|
| `context_analyze.txt` | Context synthesis |
| `executive_summary.txt` | Executive summary generation |

## 8. Engineering Conventions

- **No application imports.** This library must remain importable without pulling in application-level dependencies.
- **All exported functions take `ctx context.Context` first.**
- **Error wrapping:** `fmt.Errorf("context: %w", err)`. Inspect with `errors.Is`/`errors.As`.
- **Files ≤ 400 lines.** Refactor if a file becomes a junk drawer.
- **Logging:** `s.log` only. Debug logs must not truncate content.
- **LLM output parsing:** `parseKeyValueMap` (key/value lines). No JSON from LLM.
- **Prompt safety:** `wrapAsUserData()` around all user-origin strings in prompts.
- **Blueprint sync:** Update this file in the same commit as any change that adds or removes exported types, functions, or node types.

## 9. Domain-Segregated Interfaces

`Store` exposes six domain-specific interfaces via getter methods. Accept the narrowest interface your code actually needs.

| Getter | Interface | Purpose |
|--------|-----------|---------|
| `s.Entries()` | `EntryStore` | Episodic log CRUD and search |
| `s.Knowledge()` | `KnowledgeStore` | Semantic knowledge graph CRUD |
| `s.Graph()` | `GraphStore` | Graph traversal and vector search |
| `s.Tasks()` | `TaskStore` | Task CRUD and LLM decomposition |
| `s.Agent()` | `AgentOps` | Pending questions and query logging |
| `s.Admin()` | `AdminOps` | Maintenance and migrations |

### Option Structs

- `UpsertOptions` — consolidates optional fields for `KnowledgeStore.Upsert`
- `SearchOptions` — consolidates limit and significance threshold for `GraphStore.QuerySimilar`

### Name collision note

Several domain interfaces keep original Store method names (e.g. `GetEntry`, `GetTask`,
`CreateContext`) rather than short names. This is required because Go does not allow two methods
with the same name on the same type, even with different signatures.
