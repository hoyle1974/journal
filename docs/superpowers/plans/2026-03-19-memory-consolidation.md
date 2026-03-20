# Memory Consolidation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consolidate `pkg/journal`, `pkg/task`, and the standalone `pending_questions`/`queries` Firestore collections into a single `pkg/memory` package backed by the existing `journal` Firestore collection.

**Architecture:** All node-typed data lives in the `journal` collection, discriminated by `node_type`. New constants (`task`, `query`, `pending_question`, `log`) are added to `schema.go`. New domain files are added to `pkg/memory`, callers are updated to use them, then `pkg/journal` and `pkg/task` are deleted.

**Tech Stack:** Go, Firestore (`cloud.google.com/go/firestore`), `infra.ToolEnv`, `StartSpan`, `LoggerFrom(ctx)`

---

## Working Directory

**All edits must use the worktree path:** `/Users/jstrohm/code/jot-memory-consolidation`

Verify it exists:
```bash
git worktree list
# Expected: /Users/jstrohm/code/jot-memory-consolidation ... [feature/memory-consolidation]
```

---

## Parallel Execution Map

This plan is structured into 4 phases. Within phases 2 and 3, independent tasks can be dispatched to parallel agents — each touches different files.

```
Phase 1 (1 agent)
  └─ Task 1: Schema additions

Phase 2 (up to 4 parallel agents, after Phase 1 commits)
  ├─ Task 2: entry_nodes.go + entry_nodes_extended.go + entry_format.go
  ├─ Task 3: analysis.go
  ├─ Task 4: task_nodes.go (includes GetChildTasks, BrainstormSubtasks)
  └─ Task 5+6: query_nodes.go + pending.go migration

Phase 3 (3 parallel agents, after all Phase 2 agents commit)
  ├─ Task 7: Update pkg/memory internal files (incubation, context, rag)
  ├─ Task 8: Update internal/agent layer
  └─ Task 9: Update internal/tools + api + service + cmd

Phase 4 (sequential, after Phase 3)
  ├─ Task 10: Delete pkg/journal + pkg/task, final build
  ├─ Task 11: Update Firestore indexes
  └─ Task 12: Update docs
```

**File ownership per parallel task (no overlaps within a phase):**

| Phase 2 Agent | Files it creates/touches |
|---|---|
| Task 2 | `pkg/memory/entry_nodes.go`, `entry_nodes_extended.go`, `entry_format.go`, `entry_nodes_test.go` |
| Task 3 | `pkg/memory/analysis.go` |
| Task 4 | `pkg/memory/task_nodes.go`, `task_nodes_test.go` |
| Task 5+6 | `pkg/memory/query_nodes.go`, `query_nodes_test.go`, `pending.go` (modify) |

| Phase 3 Agent | Files it touches |
|---|---|
| Task 7 | `pkg/memory/incubation.go`, `context.go`, `rag.go` |
| Task 8 | `internal/agent/*` |
| Task 9 | `internal/tools/impl/*`, `internal/api/*`, `internal/service/*`, `cmd/*` |

---

## File Structure

**New files (pkg/memory/):**
- `entry_nodes.go` — `Entry` struct + all entry CRUD (ported from `pkg/journal/entries.go`; `*firestore.Client` → `infra.ToolEnv`)
- `entry_nodes_extended.go` — `EntryWithAnalysis` + `GetEntriesWithAnalysisByDateRange`, `QuerySimilarEntries`, `BackfillEntryEmbeddings` (ported from `pkg/journal/entries_extended.go`; `*firestore.Client` → `infra.ToolEnv`)
- `entry_format.go` — `FormatEntriesForContext`, `FormatQueriesForContext`, `TruncateTimestamp`, `DateDisplayLen`, `DateTimeDisplayLen` (ported from `pkg/journal/format.go`; no sig change — pure formatting)
- `task_nodes.go` — `Task` struct + all task CRUD + `BrainstormSubtasks` + `GetChildTasks` (ported from `pkg/task/tasks.go` + `engine.go` + `schema.go`; collection `tasks` → `journal`, `node_type=task`)
- `analysis.go` — `JournalAnalysis`, `Entity`, `OpenLoop`, `AnalyzeJournalEntry` (ported from `pkg/journal/analysis.go`; pure package rename)
- `query_nodes.go` — `QueryLog` struct + all query functions (ported from `pkg/journal/queries.go`; collection `queries` → `journal`, `node_type=query`)

**Modified files (pkg/memory/):**
- `schema.go` — add `NodeTypeTask`, `NodeTypeQuery`, `NodeTypePendingQuestion`, `NodeTypeLog`, `TaskStatusPending/Active/Completed/Abandoned` constants
- `pending.go` — change collection from `pending_questions` → `KnowledgeCollection`; add `node_type=pending_question` to writes/queries
- `incubation.go` — remove `pkg/journal` import; use `memory.GetEntriesWithAnalysisByDateRange` directly (same package, no prefix)
- `context.go` — same
- `rag.go` — same; update `[]journal.Entry` → `[]Entry` in `FuseEntries` signature and body

**Modified callers (internal/):**
- `internal/tools/impl/task_tools.go`, `journal_tools.go`, `query_tools.go`, `image_tools.go`, `context_tools.go`, `specialist_tools.go`, `memory_tools.go`, `helpers.go`, `tools_test.go`
- `internal/agent/process_entry.go`, `graph_builder.go`, `dreamer.go`, `dreamer_synthesis.go`, `rollup.go`, `foh_helpers.go`, `prompter.go`
- `internal/api/handler_tasks.go`
- `internal/service/journal_service.go`
- `cmd/jot/main.go`

**Deleted packages:**
- `pkg/journal/` (entire directory)
- `pkg/task/` (entire directory)

**Modified config:**
- `firestore.indexes.json` — add task/query/pending_question indexes on `journal`; remove stale `tasks` + `queries` collection indexes

---

## Critical Rules (Read Before Writing Any Code)

1. **Logging:** `LoggerFrom(ctx)`, never `fmt.Print` or `slog`
2. **Errors:** Always wrap with `%w`, never `%v`
3. **Spans:** Every exported function with DB I/O needs `StartSpan` / `defer span.End()`
4. **Env guard:** Every function taking `infra.ToolEnv` must check `env == nil` first
5. **Debug logs:** Never truncate at Debug level — pass full strings
6. **File size:** Keep files under 400 lines; split if needed
7. **Collection:** All new writes go to `KnowledgeCollection = "journal"` (defined in `knowledge.go`)
8. **Significance weights:** tasks = `0.7`, queries = `0.1`, pending_questions = `0.1`

---

## Phase 1

### Task 1: Schema Additions

**Files:**
- Modify: `pkg/memory/schema.go`
- Modify: `pkg/memory/schema_test.go`

This is the gate for all Phase 2 work. Must be committed before dispatching parallel agents.

- [ ] **Step 1.1: Write a failing test**

  Open `pkg/memory/schema_test.go` (exists already). Add:

  ```go
  func TestNewNodeTypeConstants(t *testing.T) {
      if NodeTypeTask != "task" {
          t.Errorf("expected NodeTypeTask == 'task', got %q", NodeTypeTask)
      }
      if NodeTypeQuery != "query" {
          t.Errorf("expected NodeTypeQuery == 'query', got %q", NodeTypeQuery)
      }
      if NodeTypePendingQuestion != "pending_question" {
          t.Errorf("expected NodeTypePendingQuestion == 'pending_question', got %q", NodeTypePendingQuestion)
      }
      if NodeTypeLog != "log" {
          t.Errorf("expected NodeTypeLog == 'log', got %q", NodeTypeLog)
      }
  }

  func TestTaskStatusConstants(t *testing.T) {
      if TaskStatusPending != "pending" {
          t.Errorf("got %q", TaskStatusPending)
      }
      if TaskStatusActive != "active" {
          t.Errorf("got %q", TaskStatusActive)
      }
      if TaskStatusCompleted != "completed" {
          t.Errorf("got %q", TaskStatusCompleted)
      }
      if TaskStatusAbandoned != "abandoned" {
          t.Errorf("got %q", TaskStatusAbandoned)
      }
  }
  ```

- [ ] **Step 1.2: Run to confirm it fails**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run "TestNewNodeTypeConstants|TestTaskStatusConstants" -v
  ```
  Expected: FAIL — constants undefined

- [ ] **Step 1.3: Add to schema.go**

  In the existing `const` block (after `NodeTypeUserIdentity`), add:

  ```go
  // NodeTypeLog is for episodic log entries (raw ingest from all channels).
  NodeTypeLog = "log"
  // NodeTypeTask is for task/todo items managed by the task engine.
  NodeTypeTask = "task"
  // NodeTypeQuery is for logged queries (Q&A pairs) from the FOH loop.
  NodeTypeQuery = "query"
  // NodeTypePendingQuestion is for gap/contradiction questions from the Dreamer.
  NodeTypePendingQuestion = "pending_question"
  ```

  Add a new `const` block (after the status block or at end of file):

  ```go
  // Task status values.
  const (
      TaskStatusPending   = "pending"
      TaskStatusActive    = "active"
      TaskStatusCompleted = "completed"
      TaskStatusAbandoned = "abandoned"
  )
  ```

- [ ] **Step 1.4: Run test**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run "TestNewNodeTypeConstants|TestTaskStatusConstants" -v
  ```
  Expected: PASS

- [ ] **Step 1.5: Build check**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go build ./pkg/memory/...
  ```

- [ ] **Step 1.6: Commit**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add pkg/memory/schema.go pkg/memory/schema_test.go
  git commit -m "feat(memory): add task/query/pending_question/log node type constants"
  ```

---

## Phase 2 — Parallel Tasks (dispatch after Task 1 is committed)

### Task 2: Create entry_nodes.go, entry_nodes_extended.go, entry_format.go

Port all three journal entry files. Key API change: swap `*firestore.Client` for `infra.ToolEnv`.

**Files:**
- Source: `pkg/journal/entries.go`, `entries_extended.go`, `format.go`
- Create: `pkg/memory/entry_nodes.go`, `entry_nodes_extended.go`, `entry_format.go`
- Create: `pkg/memory/entry_nodes_test.go`

- [ ] **Step 2.1: Read all three source files**

  ```bash
  # Read each to understand full API before writing
  ```
  - `pkg/journal/entries.go` — `Entry` struct, `AddEntry`, `UpdateEntryAudio`, `GetEntries`, `GetEntriesAsc`, `GetEntriesByDateRange`, `SearchEntries`, `CountEntries`, `GetEntry`, `DeleteEntry`
  - `pkg/journal/entries_extended.go` — `EntryWithAnalysis`, `GetEntriesWithAnalysisByDateRange`, `QuerySimilarEntries`, `BackfillEntryEmbeddings`
  - `pkg/journal/format.go` — `FormatEntriesForContext`, `FormatQueriesForContext`, `TruncateTimestamp`, `DateDisplayLen`, `DateTimeDisplayLen`

- [ ] **Step 2.2: Write a failing test**

  Create `/Users/jstrohm/code/jot-memory-consolidation/pkg/memory/entry_nodes_test.go`:

  ```go
  package memory

  import (
      "context"
      "testing"
  )

  func TestAddEntry_NilEnv(t *testing.T) {
      _, err := AddEntry(context.Background(), nil, "hello", "test", nil, "")
      if err == nil {
          t.Fatal("expected error for nil env, got nil")
      }
  }

  func TestAddEntry_EmptyContent(t *testing.T) {
      _, err := AddEntry(context.Background(), nil, "", "test", nil, "")
      if err == nil {
          t.Fatal("expected error for empty content, got nil")
      }
  }

  func TestGetEntries_NilEnv(t *testing.T) {
      _, err := GetEntries(context.Background(), nil, 10)
      if err == nil {
          t.Fatal("expected error for nil env, got nil")
      }
  }

  func TestQuerySimilarEntries_NilEnv(t *testing.T) {
      _, err := QuerySimilarEntries(context.Background(), nil, []float32{0.1, 0.2}, 5)
      if err == nil {
          t.Fatal("expected error for nil env, got nil")
      }
  }

  func TestFormatEntriesForContext_Empty(t *testing.T) {
      result := FormatEntriesForContext(nil, 1000)
      if result != "No entries found." {
          t.Errorf("expected 'No entries found.', got %q", result)
      }
  }

  func TestFormatQueriesForContext_Empty(t *testing.T) {
      result := FormatQueriesForContext(nil, 1000)
      if result != "No queries found." {
          t.Errorf("expected 'No queries found.', got %q", result)
      }
  }
  ```

- [ ] **Step 2.3: Run to confirm it fails**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run "TestAddEntry|TestGetEntries|TestQuerySimilarEntries|TestFormatEntries|TestFormatQueries" -v
  ```
  Expected: FAIL

- [ ] **Step 2.4: Create entry_nodes.go**

  Create `/Users/jstrohm/code/jot-memory-consolidation/pkg/memory/entry_nodes.go`.

  Port from `pkg/journal/entries.go` with these changes:
  - `package journal` → `package memory`
  - All functions: replace `client *firestore.Client` param with `env infra.ToolEnv`; add `if env == nil { return ..., fmt.Errorf("env is required") }` guard; replace `client` usage with `client, err := env.Firestore(ctx); if err != nil { return ..., err }`
  - Replace `EntriesCollection` → `KnowledgeCollection` (already defined in `knowledge.go`)
  - Remove `import "cloud.google.com/go/firestore"` from the EntriesCollection ref (use `KnowledgeCollection` from same package)
  - Keep `import "cloud.google.com/go/firestore"` for `firestore.Desc/Asc`, `firestore.Update`, `firestore.Query`

  Functions to port: `AddEntry`, `UpdateEntryAudio`, `GetEntries`, `GetEntriesAsc`, `GetEntriesByDateRange`, `SearchEntries`, `CountEntries`, `GetEntry`, `DeleteEntry`.

  > **Line count:** `entry_nodes.go` will be ~250 lines — well within the 400-line limit.

- [ ] **Step 2.5: Create entry_nodes_extended.go**

  Create `/Users/jstrohm/code/jot-memory-consolidation/pkg/memory/entry_nodes_extended.go`.

  Port from `pkg/journal/entries_extended.go` with these changes:
  - `package journal` → `package memory`
  - Functions: `GetEntriesWithAnalysisByDateRange`, `QuerySimilarEntries`, `BackfillEntryEmbeddings`
  - Change signatures from `client *firestore.Client` to `env infra.ToolEnv`; for `BackfillEntryEmbeddings`, signature becomes `(ctx, env infra.ToolEnv, limit int) (int, error)` — get `projectID` via `env.Config().GoogleCloudProject` inside the function
  - `EntryWithAnalysis` type moves here (it references `Entry` and `JournalAnalysis` — both now in same package)
  - Replace `journal.JournalAnalysis` → `JournalAnalysis` (same package), `journal.Entry` → `Entry`
  - Replace `EntriesCollection` → `KnowledgeCollection`
  - Replace `NormalizeEntityStatus` — already in same package in `analysis.go`

- [ ] **Step 2.6: Create entry_format.go**

  Create `/Users/jstrohm/code/jot-memory-consolidation/pkg/memory/entry_format.go`.

  Port from `pkg/journal/format.go` with only these changes:
  - `package journal` → `package memory`
  - `Entry` and `QueryLog` references work as-is (same package)

  Content:
  ```go
  package memory

  import (
      "fmt"
      "strings"
      "unicode/utf8"

      "github.com/jackstrohm/jot/pkg/utils"
  )

  const (
      DateDisplayLen     = 10
      DateTimeDisplayLen = 19
  )

  // TruncateTimestamp truncates ts for display (date 10 or datetime 19 runes).
  func TruncateTimestamp(ts string, maxLen int) string {
      return utils.TruncateString(ts, maxLen)
  }

  // FormatEntriesForContext formats entries into a readable string for LLM context.
  func FormatEntriesForContext(entries []Entry, maxChars int) string {
      if len(entries) == 0 {
          return "No entries found."
      }
      var lines []string
      totalRunes := 0
      for i, e := range entries {
          ts := e.Timestamp
          if ts == "" {
              ts = "(no date)"
          } else {
              ts = utils.TruncateString(ts, 19)
          }
          content := utils.SanitizePrompt(e.Content)
          line := fmt.Sprintf("[%s] (%s) %s", ts, e.Source, content)
          if e.ImageURL != "" {
              if e.ParsedImageDescription != "" {
                  desc := utils.SanitizePrompt(e.ParsedImageDescription)
                  line += fmt.Sprintf("\n[Attached Image Content: %s]", desc)
              } else {
                  line += "\n[Attached image]"
              }
              if e.UUID != "" {
                  line += fmt.Sprintf("\n[Entry UUID: %s]", e.UUID)
              }
          }
          lineRunes := utf8.RuneCountInString(line)
          if totalRunes+lineRunes+1 > maxChars {
              lines = append(lines, fmt.Sprintf("... and %d more entries (truncated)", len(entries)-i))
              break
          }
          lines = append(lines, line)
          totalRunes += lineRunes + 1
      }
      return strings.Join(lines, "\n")
  }

  // FormatQueriesForContext formats queries into a readable string for LLM context.
  func FormatQueriesForContext(queries []QueryLog, maxChars int) string {
      if len(queries) == 0 {
          return "No queries found."
      }
      var lines []string
      totalRunes := 0
      for i, q := range queries {
          answer := utils.SanitizePrompt(q.Answer)
          if utf8.RuneCountInString(answer) > 300 {
              answer = utils.TruncateString(answer, 300) + "..."
          }
          ts := q.Timestamp
          if ts == "" {
              ts = "(no date)"
          } else {
              ts = utils.TruncateString(ts, 19)
          }
          question := utils.SanitizePrompt(q.Question)
          line := fmt.Sprintf("[%s] (%s)\n  Q: %s\n  A: %s", ts, q.Source, question, answer)
          lineRunes := utf8.RuneCountInString(line)
          if totalRunes+lineRunes+2 > maxChars {
              lines = append(lines, fmt.Sprintf("... and %d more queries (truncated)", len(queries)-i))
              break
          }
          lines = append(lines, line)
          totalRunes += lineRunes + 2
      }
      return strings.Join(lines, "\n\n")
  }
  ```

- [ ] **Step 2.7: Run tests**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run "TestAddEntry|TestGetEntries|TestQuerySimilarEntries|TestFormatEntries|TestFormatQueries" -v
  ```
  Expected: PASS

- [ ] **Step 2.8: Build check**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go build ./pkg/memory/...
  ```

- [ ] **Step 2.9: Commit**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add pkg/memory/entry_nodes.go pkg/memory/entry_nodes_extended.go pkg/memory/entry_format.go pkg/memory/entry_nodes_test.go
  git commit -m "feat(memory): add entry_nodes, entry_nodes_extended, entry_format — port all journal entry APIs"
  ```

---

### Task 3: Create analysis.go

Port `pkg/journal/analysis.go` to `pkg/memory/analysis.go`. Straight package rename — no collection changes, no signature changes.

**Files:**
- Source: `pkg/journal/analysis.go`
- Create: `pkg/memory/analysis.go`

- [ ] **Step 3.1: Read the source file**

  Read `pkg/journal/analysis.go` — note all exported types (`Entity`, `OpenLoop`, `JournalAnalysis`, `EntityStatus*` constants) and functions (`AnalyzeJournalEntry`, `NormalizeEntityStatus`, `sanitizeTag`, `parseKeyValueAnalysis`).

- [ ] **Step 3.2: Write a failing test**

  Add to `/Users/jstrohm/code/jot-memory-consolidation/pkg/memory/entry_nodes_test.go` (or create `analysis_test.go`):

  ```go
  func TestAnalyzeJournalEntry_NilEnvLongContent(t *testing.T) {
      longContent := "this is a longer journal entry that exceeds the 20 char minimum for analysis"
      _, err := AnalyzeJournalEntry(context.Background(), nil, longContent, "uuid1", "2026-03-19")
      if err == nil {
          t.Fatal("expected error for nil env with long content, got nil")
      }
  }
  ```

- [ ] **Step 3.3: Run to confirm it fails**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run TestAnalyzeJournalEntry_NilEnvLongContent -v
  ```

- [ ] **Step 3.4: Create analysis.go**

  Create `pkg/memory/analysis.go` as a verbatim copy of `pkg/journal/analysis.go` with only `package journal` changed to `package memory`. No other changes needed — all types it references (`Entity`, `JournalAnalysis`, etc.) are defined in this same file.

- [ ] **Step 3.5: Run tests + build**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run TestAnalyzeJournalEntry -v
  cd /Users/jstrohm/code/jot-memory-consolidation && go build ./pkg/memory/...
  ```

- [ ] **Step 3.6: Commit**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add pkg/memory/analysis.go
  git commit -m "feat(memory): add analysis.go — port JournalAnalysis, Entity, AnalyzeJournalEntry"
  ```

---

### Task 4: Create task_nodes.go

Port `pkg/task/tasks.go` + `engine.go` + `schema.go` into `pkg/memory/task_nodes.go`. Key changes:
- Collection: `tasks` → `KnowledgeCollection`
- All writes add `"node_type": NodeTypeTask, "significance_weight": 0.7`
- All queries add `.Where("node_type", "==", NodeTypeTask)`
- `UpdateTaskStatus` reflection entry: `journal.AddEntry(ctx, client, ...)` → `AddEntry(ctx, env, ...)`
- `GetChildTasks` and `BrainstormSubtasks` included (from `engine.go`)
- Status consts: `StatusPending/Active/Completed/Abandoned` → `TaskStatusPending/Active/Completed/Abandoned`

**Files:**
- Source: `pkg/task/tasks.go`, `engine.go`, `schema.go`
- Create: `pkg/memory/task_nodes.go`
- Create: `pkg/memory/task_nodes_test.go`

> **Line count note:** `task_nodes.go` will be ~380 lines. If it exceeds 400, move `BrainstormSubtasks` and `GetChildTasks` to `task_engine.go`.

- [ ] **Step 4.1: Read source files**

  Read all three files in `pkg/task/` before writing anything.

- [ ] **Step 4.2: Write failing tests**

  Create `/Users/jstrohm/code/jot-memory-consolidation/pkg/memory/task_nodes_test.go`:

  ```go
  package memory

  import (
      "context"
      "testing"
  )

  func TestCreateTask_NilEnv(t *testing.T) {
      _, err := CreateTask(context.Background(), nil, &Task{Content: "test"})
      if err == nil {
          t.Fatal("expected error for nil env, got nil")
      }
  }

  func TestCreateTask_EmptyContent(t *testing.T) {
      _, err := CreateTask(context.Background(), nil, &Task{})
      if err == nil {
          t.Fatal("expected error for empty content, got nil")
      }
  }

  func TestGetTask_NilEnv(t *testing.T) {
      _, err := GetTask(context.Background(), nil, "some-uuid")
      if err == nil {
          t.Fatal("expected error for nil env, got nil")
      }
  }

  func TestFormatTasksForContext_Empty(t *testing.T) {
      result := FormatTasksForContext(nil, 1000)
      if result != "No tasks found." {
          t.Errorf("expected 'No tasks found.', got %q", result)
      }
  }

  func TestNormalizeTaskStatus(t *testing.T) {
      cases := []struct{ in, want string }{
          {"pending", TaskStatusPending},
          {"active", TaskStatusActive},
          {"completed", TaskStatusCompleted},
          {"abandoned", TaskStatusAbandoned},
          {"", TaskStatusPending},
          {"INVALID", TaskStatusPending},
      }
      for _, c := range cases {
          if got := NormalizeTaskStatus(c.in); got != c.want {
              t.Errorf("NormalizeTaskStatus(%q) = %q, want %q", c.in, got, c.want)
          }
      }
  }
  ```

- [ ] **Step 4.3: Run to confirm it fails**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run "TestCreateTask|TestGetTask|TestFormatTasksForContext|TestNormalizeTaskStatus" -v
  ```

- [ ] **Step 4.4: Create task_nodes.go**

  Create `/Users/jstrohm/code/jot-memory-consolidation/pkg/memory/task_nodes.go`.

  **Package header:**
  ```go
  package memory

  import (
      "context"
      "fmt"
      "strings"
      "time"

      "cloud.google.com/go/firestore"
      "github.com/jackstrohm/jot/internal/infra"
      "github.com/jackstrohm/jot/pkg/utils"
      "google.golang.org/api/iterator"
      "google.golang.org/genai"
  )
  ```

  **`Task` struct** — verbatim copy from `pkg/task/schema.go`, package changed.

  **Status helper and idempotency window:**
  ```go
  const taskCreateIdempotencyWindow = 30 * time.Second

  // NormalizeTaskStatus returns a valid task status.
  func NormalizeTaskStatus(s string) string {
      switch s {
      case TaskStatusPending, TaskStatusActive, TaskStatusCompleted, TaskStatusAbandoned:
          return s
      }
      return TaskStatusPending
  }
  ```

  **`normalizeContentForDedup`** — copy from `pkg/task/tasks.go` (unexported, same logic).

  **`findRecentDuplicateTask`** — copy but change:
  - `client.Collection(TasksCollection)` → `client.Collection(KnowledgeCollection)`
  - Add `.Where("node_type", "==", NodeTypeTask)` before `Where("journal_entry_ids", ...)`

  **`CreateTask`** — copy but:
  - Add `"node_type": NodeTypeTask` to doc map
  - Add `"significance_weight": 0.7` to doc map
  - Change `client.Collection(TasksCollection)` → `client.Collection(KnowledgeCollection)`
  - Change `normalizeStatus` → `NormalizeTaskStatus`

  **`GetTask`**, **`UpdateTask`**, **`UpdateTaskStatus`**, **`GetOpenRootTasks`**, **`QuerySimilarTasks`**, **`FormatTasksForContext`** — copy from `pkg/task/tasks.go` with:
  - `client.Collection(TasksCollection)` → `client.Collection(KnowledgeCollection)`
  - Add `.Where("node_type", "==", NodeTypeTask)` before OrderBy/FindNearest chains
  - In `UpdateTaskStatus`: replace `journal.AddEntry(ctx, client, summary, "system:task_engine", nil, "")` → `AddEntry(ctx, env, summary, "system:task_engine", nil, "")` (same package, no prefix; `client` not needed since `AddEntry` now takes `env`)
  - Replace `StatusCompleted/Abandoned/etc` → `TaskStatusCompleted/Abandoned/etc`
  - Replace `normalizeStatus` → `NormalizeTaskStatus`

  **`BrainstormSubtasks`** — copy from `pkg/task/engine.go`. Already calls `GetTask` and `CreateTask` which are now in same package. Change `StatusPending` → `TaskStatusPending`.

  **`GetChildTasks`** — copy from `pkg/task/engine.go`. Change:
  - `client.Collection(TasksCollection)` → `client.Collection(KnowledgeCollection)`
  - Add `.Where("node_type", "==", NodeTypeTask)` before `Where("parent_id", ...)`
  - `StatusPending` → `TaskStatusPending`, `StatusActive` → `TaskStatusActive`

  **`UpdateTaskOpts`** — copy from `pkg/task/tasks.go` (the struct definition).

  **`applyAddRemove`** — copy helper (unexported).

- [ ] **Step 4.5: Run tests + build**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run "TestCreateTask|TestGetTask|TestFormatTasksForContext|TestNormalizeTaskStatus" -v
  cd /Users/jstrohm/code/jot-memory-consolidation && go build ./pkg/memory/...
  ```

- [ ] **Step 4.6: Commit**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add pkg/memory/task_nodes.go pkg/memory/task_nodes_test.go
  git commit -m "feat(memory): add task_nodes.go — port task engine to journal collection with node_type=task"
  ```

---

### Task 5+6: Create query_nodes.go + Migrate pending.go

These two files are owned by the same agent since `query_nodes.go` is small and `pending.go` is a minor migration.

**Files:**
- Source: `pkg/journal/queries.go`
- Create: `pkg/memory/query_nodes.go`, `query_nodes_test.go`
- Modify: `pkg/memory/pending.go`

- [ ] **Step 5.1: Write failing tests**

  Create `/Users/jstrohm/code/jot-memory-consolidation/pkg/memory/query_nodes_test.go`:

  ```go
  package memory

  import (
      "context"
      "testing"
  )

  func TestSaveQuery_NilEnv(t *testing.T) {
      _, err := SaveQuery(context.Background(), nil, "q?", "a", "test", false)
      if err == nil {
          t.Fatal("expected error for nil env, got nil")
      }
  }

  func TestGetRecentQueries_NilEnv(t *testing.T) {
      _, err := GetRecentQueries(context.Background(), nil, 10)
      if err == nil {
          t.Fatal("expected error for nil env, got nil")
      }
  }
  ```

- [ ] **Step 5.2: Run to confirm it fails**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run "TestSaveQuery|TestGetRecentQueries_NilEnv" -v
  ```

- [ ] **Step 5.3: Create query_nodes.go**

  Create `/Users/jstrohm/code/jot-memory-consolidation/pkg/memory/query_nodes.go`.

  Port from `pkg/journal/queries.go` with:
  - `package journal` → `package memory`
  - All functions: replace `client *firestore.Client` param with `env infra.ToolEnv`; add nil guard; get client via `env.Firestore(ctx)`
  - `client.Collection(QueriesCollection)` → `client.Collection(KnowledgeCollection)`
  - All writes add `"node_type": NodeTypeQuery, "significance_weight": 0.1`
  - All `OrderBy`/`Where` queries add `.Where("node_type", "==", NodeTypeQuery)` before other filters

  Functions to port: `SaveQuery`, `GetRecentQueries`, `SearchQueries`, `GetRecentGapQueries`, `GetQueriesByDateRange`.

  Keep `QueryLog` struct definition exactly as in `queries.go` (same fields, just package changes).

- [ ] **Step 5.4: Run query tests + build**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run "TestSaveQuery|TestGetRecentQueries_NilEnv" -v
  cd /Users/jstrohm/code/jot-memory-consolidation && go build ./pkg/memory/...
  ```

- [ ] **Step 5.5: Run existing pending tests before modifying**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -run TestFilterDuplicate -v
  ```
  Note result — expect PASS (no Firestore dependency in dedup test).

- [ ] **Step 5.6: Migrate pending.go**

  Read `/Users/jstrohm/code/jot-memory-consolidation/pkg/memory/pending.go`.

  Make these changes:
  1. Change the constant to point to `KnowledgeCollection`:
     ```go
     // PendingQuestionsCollection is kept for call-site compatibility.
     const PendingQuestionsCollection = KnowledgeCollection
     ```
  2. In `InsertPendingQuestions`, add to the `Set` map:
     ```go
     "node_type":           NodeTypePendingQuestion,
     "significance_weight": 0.1,
     ```
  3. In `GetUnresolvedPendingQuestions`, add `node_type` filter:
     ```go
     query := client.Collection(PendingQuestionsCollection).
         Where("node_type", "==", NodeTypePendingQuestion).
         OrderBy("created_at", firestore.Desc).
         Limit(100)
     ```
  4. In `GetRecentlyResolvedPendingQuestions`, add `node_type` filter:
     ```go
     query := client.Collection(PendingQuestionsCollection).
         Where("node_type", "==", NodeTypePendingQuestion).
         Where("created_at", ">=", sinceStr).
         OrderBy("created_at", firestore.Desc).
         Limit(200)
     ```
  5. `ResolvePendingQuestion`, `GetTelegramActiveQuestion`, `SetTelegramActiveQuestion`, `ClearTelegramActiveQuestion` — these do `Doc(uuid).Get/Update/Set/Delete` by ID so no `node_type` filter needed.

  > **Ordering note:** The new `Where("node_type") + Where("created_at") + OrderBy("created_at")` queries require the composite index `(node_type ASC, created_at DESC)` on `journal`. This index is added in Task 11 (Phase 4). The migration is safe to commit now; the production queries will return a Firestore error until the index is deployed in Task 11.

- [ ] **Step 5.7: Run all memory tests**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./pkg/memory/... -v
  ```
  Expected: all pass

- [ ] **Step 5.8: Commit**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add pkg/memory/query_nodes.go pkg/memory/query_nodes_test.go pkg/memory/pending.go
  git commit -m "feat(memory): add query_nodes.go and migrate pending.go to journal collection"
  ```

---

## Phase 3 — Parallel Caller Updates (dispatch after ALL Phase 2 tasks are committed)

### Task 7: Update pkg/memory Internal Files

`incubation.go`, `context.go`, and `rag.go` currently import `pkg/journal`. Remove those imports and use the functions directly (same package, no prefix).

**Files:** `pkg/memory/incubation.go`, `context.go`, `rag.go`

- [ ] **Step 7.1: Find all journal references**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && grep -n "journal\." pkg/memory/incubation.go pkg/memory/context.go pkg/memory/rag.go
  ```

- [ ] **Step 7.2: Update incubation.go**

  Read the file. Remove `"github.com/jackstrohm/jot/pkg/journal"` from imports.

  Replace all `journal.X` usages:
  - `journal.GetEntriesWithAnalysisByDateRange(ctx, client, ...)` → `GetEntriesWithAnalysisByDateRange(ctx, env, ...)` where `env` is already available (check function signature — if it receives `client *firestore.Client`, change to `env infra.ToolEnv`)
  - `journal.Entry` → `Entry`
  - `journal.EntryWithAnalysis` → `EntryWithAnalysis`
  - `journal.JournalAnalysis` → `JournalAnalysis`

- [ ] **Step 7.3: Update context.go**

  Read the file. Apply same pattern — remove import, strip `journal.` prefix.

- [ ] **Step 7.4: Update rag.go**

  Read the file. Apply same pattern. Special case:
  - `FuseEntries` function signature likely has `[]journal.Entry` parameters → change to `[]Entry`
  - Any calls to `journal.SearchEntries`, `journal.GetEntries` → `SearchEntries`, `GetEntries`

- [ ] **Step 7.5: Build check**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go build ./pkg/memory/...
  ```
  Expected: no errors

- [ ] **Step 7.6: Commit**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add pkg/memory/incubation.go pkg/memory/context.go pkg/memory/rag.go
  git commit -m "refactor(memory): remove pkg/journal imports from pkg/memory internal files"
  ```

---

### Task 8: Update internal/agent Layer

**Files:** `process_entry.go`, `graph_builder.go`, `dreamer.go`, `dreamer_synthesis.go`, `rollup.go`, `foh_helpers.go`, `prompter.go`

- [ ] **Step 8.1: Find all usages**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && grep -rn "journal\.\|task\." internal/agent/ --include="*.go" | grep -v "_test.go"
  ```

- [ ] **Step 8.2: For each file, apply these replacements**

  Update imports: remove `pkg/journal` and `pkg/task`; add `pkg/memory` (if not already imported).

  **Replacement map:**

  | Old | New |
  |-----|-----|
  | `journal.AddEntry(ctx, client, ...)` | `memory.AddEntry(ctx, env, ...)` |
  | `journal.GetEntries(ctx, client, ...)` | `memory.GetEntries(ctx, env, ...)` |
  | `journal.GetEntriesAsc(ctx, client, ...)` | `memory.GetEntriesAsc(ctx, env, ...)` |
  | `journal.GetEntriesByDateRange(ctx, client, ...)` | `memory.GetEntriesByDateRange(ctx, env, ...)` |
  | `journal.SearchEntries(ctx, client, ...)` | `memory.SearchEntries(ctx, env, ...)` |
  | `journal.GetEntriesWithAnalysisByDateRange(ctx, client, ...)` | `memory.GetEntriesWithAnalysisByDateRange(ctx, env, ...)` |
  | `journal.AnalyzeJournalEntry(ctx, env, ...)` | `memory.AnalyzeJournalEntry(ctx, env, ...)` |
  | `journal.Entity` | `memory.Entity` |
  | `journal.JournalAnalysis` | `memory.JournalAnalysis` |
  | `journal.OpenLoop` | `memory.OpenLoop` |
  | `journal.SaveQuery(ctx, client, ...)` | `memory.SaveQuery(ctx, env, ...)` |
  | `journal.FormatEntriesForContext(...)` | `memory.FormatEntriesForContext(...)` |
  | `journal.FormatQueriesForContext(...)` | `memory.FormatQueriesForContext(...)` |
  | `journal.DateDisplayLen` | `memory.DateDisplayLen` |
  | `journal.TruncateTimestamp(...)` | `memory.TruncateTimestamp(...)` |
  | `task.CreateTask(ctx, env, ...)` | `memory.CreateTask(ctx, env, ...)` |
  | `task.GetTask(ctx, env, ...)` | `memory.GetTask(ctx, env, ...)` |
  | `task.UpdateTask(ctx, env, ...)` | `memory.UpdateTask(ctx, env, ...)` |
  | `task.UpdateTaskStatus(ctx, env, ...)` | `memory.UpdateTaskStatus(ctx, env, ...)` |
  | `task.GetOpenRootTasks(ctx, env, ...)` | `memory.GetOpenRootTasks(ctx, env, ...)` |
  | `task.GetChildTasks(ctx, env, ...)` | `memory.GetChildTasks(ctx, env, ...)` |
  | `task.QuerySimilarTasks(ctx, env, ...)` | `memory.QuerySimilarTasks(ctx, env, ...)` |
  | `task.FormatTasksForContext(...)` | `memory.FormatTasksForContext(...)` |
  | `task.Task{}` | `memory.Task{}` |
  | `task.UpdateTaskOpts{}` | `memory.UpdateTaskOpts{}` |
  | `task.NormalizeStatus(...)` | `memory.NormalizeTaskStatus(...)` |
  | `task.StatusPending` | `memory.TaskStatusPending` |
  | `task.StatusActive` | `memory.TaskStatusActive` |
  | `task.StatusCompleted` | `memory.TaskStatusCompleted` |
  | `task.StatusAbandoned` | `memory.TaskStatusAbandoned` |

  > **Callers that use `client *firestore.Client`:** If a function currently receives a `*firestore.Client` and passes it to `journal.AddEntry(ctx, client, ...)`, change that parameter to `env infra.ToolEnv` and call `memory.AddEntry(ctx, env, ...)`. In most cases in `internal/agent/`, `env` is already available on the struct or passed as a parameter.

- [ ] **Step 8.3: Build check for agent layer**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go build ./internal/agent/...
  ```
  Fix remaining errors before committing.

- [ ] **Step 8.4: Commit**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add internal/agent/
  git commit -m "refactor(agent): update imports from pkg/journal and pkg/task to pkg/memory"
  ```

---

### Task 9: Update Tools, API, Service, cmd Layers

**Files:**
- `internal/tools/impl/task_tools.go`, `journal_tools.go`, `query_tools.go`, `image_tools.go`, `context_tools.go`, `specialist_tools.go`, `memory_tools.go`, `helpers.go`, `tools_test.go`
- `internal/api/handler_tasks.go`
- `internal/service/journal_service.go`
- `cmd/jot/main.go`

- [ ] **Step 9.1: Find all usages**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && grep -rn "\"github.com/jackstrohm/jot/pkg/journal\"\|\"github.com/jackstrohm/jot/pkg/task\"" internal/tools/ internal/api/ internal/service/ cmd/
  ```

- [ ] **Step 9.2: Update each file**

  Use the same replacement map as Task 8.

  **Additional notes:**
  - `query_tools.go`: callers of `journal.SaveQuery(ctx, client, ...)` must change to `memory.SaveQuery(ctx, env, ...)`. If the calling code manually calls `env.Firestore(ctx)` to get a client just for this, remove that step — `memory.SaveQuery` handles it internally.
  - `task_tools.go`: `task.BrainstormSubtasks` → `memory.BrainstormSubtasks`
  - `tools_test.go`: likely imports `journal` for `journal.Entry` or similar — swap to `memory.Entry`

- [ ] **Step 9.3: Full project build**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go build ./...
  ```
  Expected: no errors. If errors remain, grep for remaining `pkg/journal` and `pkg/task` imports.

- [ ] **Step 9.4: Commit**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add internal/tools/ internal/api/ internal/service/ cmd/
  git commit -m "refactor(tools,api,service,cmd): update imports from pkg/journal and pkg/task to pkg/memory"
  ```

---

## Phase 4 — Finalization (sequential, after ALL Phase 3 agents commit)

### Task 10: Delete Old Packages + Final Verification

- [ ] **Step 10.1: Confirm no remaining callers**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && grep -rn "\"github.com/jackstrohm/jot/pkg/journal\"\|\"github.com/jackstrohm/jot/pkg/task\"" --include="*.go" .
  ```
  Expected: zero results (besides the packages' own files).

- [ ] **Step 10.2: Delete packages**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && rm -rf pkg/journal/ pkg/task/
  ```

- [ ] **Step 10.3: Full build**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go build ./...
  ```
  Expected: no errors. If errors remain, a caller was missed — find with `grep -rn "journal\.\|task\."` and fix.

- [ ] **Step 10.4: Run all tests**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go test ./...
  ```

- [ ] **Step 10.5: go vet**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go vet ./...
  ```

- [ ] **Step 10.6: Commit**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add -A
  git commit -m "refactor: delete pkg/journal and pkg/task — memory consolidation complete"
  ```

---

### Task 11: Update Firestore Indexes

**Files:** `firestore.indexes.json`

- [ ] **Step 11.1: Read current indexes**

  Read `firestore.indexes.json`. Note the existing `journal` collection indexes — particularly that `(node_type ASC, timestamp DESC)` already exists.

- [ ] **Step 11.2: Remove stale indexes**

  Remove these entries from `"indexes"`:
  - The entire `"collectionGroup": "tasks"` vector index (2 entries — the embedding-only vector and the `journal_entry_ids ARRAY + timestamp DESC`)
  - The entire `"collectionGroup": "queries"` index (1 entry — `is_gap + timestamp`)

- [ ] **Step 11.3: Add new indexes**

  Add to the `"indexes"` array:

  ```json
  {
    "collectionGroup": "journal",
    "queryScope": "COLLECTION",
    "fields": [
      { "fieldPath": "node_type", "order": "ASCENDING" },
      { "fieldPath": "journal_entry_ids", "arrayConfig": "CONTAINS" },
      { "fieldPath": "timestamp", "order": "DESCENDING" }
    ]
  },
  {
    "collectionGroup": "journal",
    "queryScope": "COLLECTION",
    "fields": [
      { "fieldPath": "node_type", "order": "ASCENDING" },
      { "fieldPath": "is_gap", "order": "ASCENDING" },
      { "fieldPath": "timestamp", "order": "DESCENDING" }
    ]
  },
  {
    "collectionGroup": "journal",
    "queryScope": "COLLECTION",
    "fields": [
      { "fieldPath": "node_type", "order": "ASCENDING" },
      { "fieldPath": "created_at", "order": "DESCENDING" }
    ]
  },
  {
    "collectionGroup": "journal",
    "queryScope": "COLLECTION",
    "fields": [
      { "fieldPath": "node_type", "order": "ASCENDING" },
      { "fieldPath": "parent_id", "order": "ASCENDING" },
      { "fieldPath": "timestamp", "order": "ASCENDING" }
    ]
  }
  ```

  > **Verify `(node_type, timestamp)` index already exists** — if the `journal` collection already has a composite index on `(node_type ASC, timestamp DESC)` (used by `GetEntriesByDateRange` etc.), do not add a duplicate.

- [ ] **Step 11.4: Deploy indexes**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && firebase deploy --only firestore:indexes
  ```

- [ ] **Step 11.5: Commit**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add firestore.indexes.json
  git commit -m "feat(infra): update firestore indexes for task/query/pending_question on journal collection"
  ```

---

### Task 12: Update Docs

**Files:** `internal/prompts/app_capabilities.txt`, `blueprint.md`, `briefs/active/20260319_memory-consolidation.md`

- [ ] **Step 12.1: Read both docs**

  Read `internal/prompts/app_capabilities.txt` and `blueprint.md`.

- [ ] **Step 12.2: Update app_capabilities.txt**

  Find and update:
  - "tasks collection" → "journal collection (node_type=task)"
  - "queries collection" → "journal collection (node_type=query)"
  - "pending_questions collection" → "journal collection (node_type=pending_question)"
  - Any mention of `pkg/journal` or `pkg/task` as separate packages → `pkg/memory`

- [ ] **Step 12.3: Update blueprint.md**

  Update any architecture descriptions that reference `pkg/journal` or `pkg/task` as separate packages.

- [ ] **Step 12.4: Final build + test**

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && go build ./... && go test ./...
  ```

- [ ] **Step 12.5: Update brief and commit**

  In `briefs/active/20260319_memory-consolidation.md`, set status to `done` and add session log entry.

  ```bash
  cd /Users/jstrohm/code/jot-memory-consolidation && git add internal/prompts/app_capabilities.txt blueprint.md
  git commit -m "docs: update app_capabilities and blueprint for unified pkg/memory"
  ```

---

## Finish: Merge to main

After Phase 4 completes and `go build ./... && go test ./...` passes cleanly:

```bash
cd /Users/jstrohm/code/jot
git checkout main
git merge feature/memory-consolidation
git worktree remove ../jot-memory-consolidation
mv briefs/active/20260319_memory-consolidation.md briefs/done/20260319_memory-consolidation.md
git add briefs/
git commit -m "chore: close memory-consolidation brief"
```
