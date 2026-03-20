# Memory Package Extraction Design

**Date:** 2026-03-20
**Status:** Approved
**Goal:** Refactor `pkg/memory` from a collection of package-level functions tightly coupled to `internal/infra` into a self-contained `MemoryStore` struct with clean dependency injection — suitable for eventual extraction into a standalone Go module.

---

## Context

`pkg/memory` is the core storage and retrieval layer for Jot's GraphRAG system. It contains ~4,700 lines across 20 files and exports 70+ functions covering: journal entries, knowledge nodes, tasks, contexts, pending questions, query logs, analysis, graph expansion, and incubation.

Currently every function accepts `env infra.ToolEnv` as a parameter, coupling the package to jot's internal infrastructure (Firestore client, Gemini embeddings, LLM dispatch, logging, spans, UUID generation, Firestore query wrappers). This makes the package non-portable and hard to test in isolation.

---

## Goals

- **Primary (now):** Clean internal boundaries — break the `infra.ToolEnv` coupling, make the package self-contained and independently testable
- **Secondary (future):** Publishable as a standalone Go module with minimal further changes

---

## Non-Goals

- Pluggable storage backends (Firestore remains concrete)
- Replacing Firestore with a graph or vector database
- Breaking API compatibility with jot callers beyond the mechanical migration in Phase 2

---

## Design

### 1. The `Store` Struct

A single `Store` struct replaces all package-level functions. All 70+ functions become methods on `Store`.

```go
// pkg/memory/memory.go

type Store struct {
    db       *firestore.Client
    embedder Embedder
    llm      LLMDispatcher
    log      *slog.Logger // nil = no-op logger
}

func New(db *firestore.Client, embedder Embedder, llm LLMDispatcher, opts ...Option) *Store

func WithLogger(l *slog.Logger) Option
```

Note: `projectID` is **not** a field on `Store`. It is captured at construction time inside the `Embedder` and `LLMDispatcher` implementations (see Section 2). All embedding and dispatch call sites in the package call the injected interface — the concrete Gemini implementations own the project ID and model name internally.

Method signatures change from:
```go
func GetEntry(ctx context.Context, env infra.ToolEnv, id string) (*Entry, error)
```
to:
```go
func (s *Store) GetEntry(ctx context.Context, id string) (*Entry, error)
```

This is a purely mechanical change — no logic moves.

### 2. The Two Interfaces

```go
// pkg/memory/interfaces.go

type Embedder interface {
    GenerateEmbedding(ctx context.Context, text string, taskType string) ([]float32, error)
    GenerateEmbeddingsBatch(ctx context.Context, texts []string, taskType string) ([][]float32, error)
}

type LLMDispatcher interface {
    Dispatch(ctx context.Context, req LLMRequest) (string, error)
}

type LLMRequest struct {
    SystemPrompt string
    UserPrompt   string
    MaxTokens    int
    JSONMode     bool
}
```

**`LLMRequest` field completeness:** All `MaxOutputTokens` values used by the memory package's dispatch sites were audited — only `MaxTokens int` and `JSONMode bool` are required. No `Temperature`, `TopP`, `ResponseMIMEType`, or `ModelOverride` fields appear anywhere in `pkg/memory`. The struct is complete as specified.

**`LLMRequest` design:** The current codebase builds `Parts: []*genai.Part{{Text: prompt}}` in three files (`analysis.go`, `context.go`, `task_engine.go`), and calls `infra.GenerateContentSimple` in `task_nodes.go` and `rerank.go`. All five are single-text prompts. The new flat `SystemPrompt`/`UserPrompt` fields replace the `Parts` pattern. All five callers are updated to use these string fields instead. Multi-part structured messages are out of scope for the initial extraction.

**`LLMDispatcher.Dispatch` returns `string`:** The Gemini implementation in `pkg/memory/gemini/dispatcher.go` extracts text from the `genai.GenerateContentResponse` before returning (equivalent to the current `infra.ExtractText(resp)` call). Empty-response errors are handled inside the implementation. Callers receive clean text or an error.

**Token floors:** The Gemini dispatcher replicates jot's production safeguards: if `MaxTokens` is 0, it defaults to 8192; a minimum floor of 4096 is enforced. This matches the current behaviour in `infra.App.Dispatch`.

**Prompt sanitization:** The `GeminiDispatcher.Dispatch` implementation does **not** call `SanitizePrompt` internally. Sanitization is the caller's responsibility, consistent with the project rule "Never trust user input strings inside a prompt." Callers in `pkg/memory` already use `WrapAsUserData` and `SanitizePrompt` where required (copied into `text.go`).

**Prometheus/embedding metrics:** The Gemini implementations in `pkg/memory/gemini/` do **not** import or record Prometheus metrics (`RecordEmbeddingPrometheusMetrics`, `LogEmbeddingStats`). These are operational concerns for the host application. In jot, the `infra.App` layer retains its own Prometheus instrumentation; the memory library's Gemini implementations are intentionally metrics-free. If jot needs per-call metrics, it can wrap the interfaces.

**Gemini implementation construction:**

```go
// pkg/memory/gemini/embedder.go
func NewEmbedder(projectID string) Embedder

// pkg/memory/gemini/dispatcher.go
func NewDispatcher(projectID string, model string) LLMDispatcher
```

Both implementations capture `projectID` and `model` at construction time — nothing is passed per-call. In jot, `infra.App` constructs these once using values from `config.Config` and stores the resulting `*memory.Store` as a field.

### 3. `EvaluateFactCollision`

`infra.EvaluateFactCollision` is called twice in `knowledge.go` to detect contradictory facts before upsert. It uses `infra.GenerateContentSimple` with a hardcoded prompt and the Gemini model from config.

This becomes a **private method on `Store`**:

```go
func (s *Store) evaluateFactCollision(ctx context.Context, fact1, fact2 string) (bool, error)
```

It dispatches through `s.llm.Dispatch(ctx, LLMRequest{...})` with an inline prompt. The logic moves out of `internal/infra` entirely and into `knowledge.go` or a new `pkg/memory/collision.go`.

### 4. Observability

- **Logging:** An optional `*slog.Logger` is injected via `WithLogger(l)`. If nil, a no-op logger (`slog.New(slog.NewTextHandler(io.Discard, nil))`) is used. All `infra.LoggerFrom(ctx)` calls are replaced with `s.log`.
- **Tracing:** All `infra.StartSpan` / `span.End()` calls are removed. Spans are the caller's responsibility.
- **Domain log helpers** (`LogVectorSearchFailed`, `LogFoundEntry`, `LogFoundNode`, `LogRAGQuality`): These contain non-trivial logic — structured message formatting, RAG confidence classification, p90 score computation. They are **copied** into `pkg/memory/log.go` as unexported functions, adapted to use `*slog.Logger` instead of the request-scoped infra logger. Behaviour is preserved.

### 5. Prompt Templates

The package imports `internal/prompts` for three LLM prompt templates used in `analysis.go` and `context.go`. The prompt **templates** (`.txt` files) and their **typed builder functions** (the `Data` structs and `Build*` functions) move into `pkg/memory/prompts/`:

```
pkg/memory/prompts/
├── embed.go                     # //go:embed directives
├── journal_analyze.go           # JournalAnalyzeData struct + BuildJournalAnalyze()
├── journal_analyze.txt
├── context_analyze.go           # ContextAnalyzeData struct + BuildContextAnalyze()
├── context_analyze.txt
└── executive_summary.go         # ExecutiveSummary() string accessor
    executive_summary.txt
```

The `internal/prompts` package retains its own copies for other jot consumers. The memory package's copies are authoritative for memory-specific operations going forward.

### 6. Utility Functions

Small utility functions currently sourced from `internal/infra` and `pkg/utils` are copied into the package:

| Function | Source | Target file |
|---|---|---|
| `CosineSimilarity` | `pkg/utils` | `pkg/memory/math.go` |
| `GenerateUUID` | `internal/infra` | Inline as `uuid.NewString()` |
| `WrapFirestoreIndexError` | `internal/infra` | `pkg/memory/errors.go` |
| `QueryDocuments` | `internal/infra` | `pkg/memory/firestore.go` |
| `GetStringField`, `GetFloat32SliceField`, etc. | `internal/infra` | `pkg/memory/firestore.go` |
| `ParseKeyValueMap`, `TruncateString`, `SanitizePrompt`, `WrapAsUserData` | `pkg/utils` | `pkg/memory/text.go` |
| RAG log helpers (`LogVectorSearchFailed`, etc.) | `internal/infra` | `pkg/memory/log.go` |

These are small, stable functions with no further internal dependencies. Source is noted in comments. After copying, `pkg/memory` has no imports from `internal/infra` or `pkg/utils`.

### 7. Telegram Generalization

`GetTelegramActiveQuestion`, `SetTelegramActiveQuestion`, `ClearTelegramActiveQuestion` reference the Telegram domain by name in a storage package. These are renamed:

```go
func (s *Store) GetActiveQuestion(ctx context.Context, clientID string) (*PendingQuestion, error)
func (s *Store) SetActiveQuestion(ctx context.Context, clientID string, questionUUID string) error
func (s *Store) ClearActiveQuestion(ctx context.Context, clientID string) error
```

The `TelegramQuestionStateCollection` constant is renamed to `ActiveQuestionStateCollection`.

**Caller migration note:** The three callers in `internal/service/telegram_process.go` currently pass `chatID int64`. This is a type change (not just a rename): callers must convert `strconv.FormatInt(chatID, 10)` before passing. This is flagged as a non-mechanical step in Phase 2.

### 8. `MigrateKnowledgeMetadata`

Currently takes `*firestore.Client` directly (inconsistent with all other functions). Becomes a method on `Store`:

```go
func (s *Store) MigrateKnowledgeMetadata(ctx context.Context, dryRun bool) (int, error)
```

### 9. Wiring in jot (`infra.App`)

After Phase 1, `infra.App` is updated to construct and expose a `*memory.Store`:

```go
// internal/infra/app.go
type App struct {
    // existing fields ...
    Memory *memory.Store
}
```

`Memory` is a **public field** (not a getter method). It is constructed once during `App` initialization using the existing Firestore client and the new `gemini.NewEmbedder` / `gemini.NewDispatcher` constructors.

Tool implementations in `internal/tools/impl/` that currently call `memory.FuncName(ctx, env, ...)` obtain the store via the `*infra.App` already passed to them (consistent with the existing pattern of passing `*infra.App` explicitly). They call `app.Memory.FuncName(ctx, ...)`.

---

## Migration Plan

### Phase 1 — Build the struct (no caller changes)

1. Create `pkg/memory/interfaces.go` — `Embedder`, `LLMDispatcher`, `LLMRequest`
2. Create `pkg/memory/gemini/` — `NewEmbedder(projectID)` and `NewDispatcher(projectID, model)` implementations
3. Copy utility functions into `pkg/memory/{math,errors,firestore,text,log}.go`
4. Move prompt templates and builder functions into `pkg/memory/prompts/`
5. Create `pkg/memory/memory.go` — `Store` struct and `New(...)` constructor with `WithLogger` option
6. Convert all 70+ package-level functions to methods on `Store`, file by file
7. Inline `evaluateFactCollision` as a private method in `knowledge.go` using `s.llm.Dispatch`
8. Generalize Telegram functions; rename `MigrateKnowledgeMetadata`

**Outcome:** `pkg/memory` compiles with no `internal/infra` or `pkg/utils` imports. Phase 1 and Phase 2 are done together in a single feature branch — there are no shims and no flag day. The branch may not build between phases but merges cleanly once Phase 2 is also complete.

### Phase 2 — Wire it up in jot (migrate 38 callers)

9. Add `Memory *memory.Store` as a public field on `infra.App`; construct it in `App` initialization
10. Migrate all 38 callers: `memory.FuncName(ctx, env, ...)` → `app.Memory.FuncName(ctx, ...)`
    - Note: Telegram callers require a type conversion (`strconv.FormatInt(chatID, 10)`) — not purely mechanical
11. Delete the old package-level function shims

**Outcome:** All jot code uses the struct API; `pkg/memory` has no references to `internal/infra`.

### Phase 3 — Standalone module (future)

12. Move `pkg/memory` to its own repository
13. Update the import path in jot
14. Tag `v0.1.0`

---

## File Layout After Phase 1

```
pkg/memory/
├── memory.go               # Store struct, New(), Option pattern
├── interfaces.go           # Embedder, LLMDispatcher, LLMRequest
├── errors.go               # WrapFirestoreIndexError (copied from infra)
├── firestore.go            # QueryDocuments, GetStringField, etc. (copied from infra)
├── math.go                 # CosineSimilarity (copied from utils)
├── text.go                 # ParseKeyValueMap, TruncateString, etc. (copied from utils)
├── log.go                  # RAG log helpers (adapted from infra, unexported)
├── prompts/
│   ├── embed.go
│   ├── journal_analyze.go  # JournalAnalyzeData + BuildJournalAnalyze()
│   ├── journal_analyze.txt
│   ├── context_analyze.go  # ContextAnalyzeData + BuildContextAnalyze()
│   ├── context_analyze.txt
│   ├── executive_summary.go
│   └── executive_summary.txt
├── gemini/
│   ├── embedder.go         # NewEmbedder(projectID string) Embedder
│   └── dispatcher.go       # NewDispatcher(projectID, model string) LLMDispatcher
├── analysis.go
├── context.go
├── entry_format.go
├── entry_nodes.go
├── entry_nodes_extended.go
├── graph.go
├── incubation.go
├── janitor.go
├── knowledge.go
├── migrate.go
├── pending.go
├── query_format.go
├── query_nodes.go
├── rag.go
├── rerank.go
├── rollup.go
├── schema.go
├── task_engine.go
├── task_nodes.go
└── task_query.go
```

---

## Testing

- Existing test files (`*_test.go`) are updated to construct a `*Store` directly with a test Firestore client, removing the need to mock `infra.ToolEnv`
- The `Embedder` and `LLMDispatcher` interfaces make it straightforward to inject fakes in unit tests without hitting Gemini
- Integration tests continue to use a real Firestore emulator as before
- `pending_dedup_test.go` calls `utils.CosineSimilarity` directly; after Phase 1 this is satisfied by the in-package copy in `math.go` (same package, self-healing)

---

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Phase 1+2 done together means branch is temporarily broken | Both phases are in one feature branch; it only merges when it compiles — no partial-state merges |
| Phase 2 (38-caller migration) is large | Mostly mechanical search-and-replace; Telegram callers are the one exception requiring a type conversion |
| Prometheus metrics lost from Gemini implementations | Intentional — operational metrics are the host app's concern; jot's infra retains its own instrumentation |
| Copying utility functions creates divergence | Functions copied are small and stable; source noted in comments |
| Prompt templates diverging from `internal/prompts` | Memory-specific templates are now the memory package's responsibility; document this explicitly |
| `evaluateFactCollision` inlining loses observability | Private method logs via `s.log`; equivalent to current behaviour |
