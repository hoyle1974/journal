# Brief: Memory Consolidation — Unify pkg/journal and pkg/task into pkg/memory

**Date:** 20260319
**Status:** `done`
**Branch:** `feature/memory-consolidation`
**Worktree:** `../jot-memory-consolidation`

---

## Goal

Consolidate `pkg/journal`, `pkg/task`, and the standalone `pending_questions`/`queries` Firestore collections into a single `pkg/memory` package backed by the existing unified `journal` Firestore collection. Tasks, queries, and pending questions become new `node_type` values, eliminating separate collections and packages. The result is a single, coherent package that owns all node-typed data.

---

## Scope

**In:**
- Add new node_types to `pkg/memory/schema.go`: `task`, `query`, `pending_question`
- Create `pkg/memory/entry_nodes.go` — port all of `pkg/journal/entries.go` (uses `*firestore.Client`, collection `journal`, `node_type=log`)
- Create `pkg/memory/task_nodes.go` — port all of `pkg/task/tasks.go` + `schema.go` + `engine.go` (uses `infra.ToolEnv`, moves from collection `tasks` → `journal`, `node_type=task`)
- Create `pkg/memory/query_nodes.go` — port all of `pkg/journal/queries.go` (moves from collection `queries` → `journal`, `node_type=query`)
- Migrate `pkg/memory/pending.go` to use the `journal` collection with `node_type=pending_question` instead of `pending_questions` collection
- Update all callers in `internal/`, `cmd/` to use new `pkg/memory` API
- Delete `pkg/journal/` and `pkg/task/` packages
- Update `firestore.indexes.json` for new query patterns on the `journal` collection
- Update `app_capabilities.txt` and `blueprint.md`

**Out:**
- No data migration (greenfield — new node_types, old data stays in old collections harmlessly)
- No storage interface / abstraction layer (Firestore hardcoded)
- No context nodes consolidation (Plan 4)
- No separate GitHub repo

---

## Approach & Key Decisions

- **Storage:** All node types land in the `journal` Firestore collection (already the home for `log` and knowledge nodes). `node_type` field is the discriminator.
- **Package name stays `pkg/memory`:** No rename. New files are added alongside existing ones.
- **`KnowledgeNode` is the storage type for tasks and queries:** Task-specific fields (`parent_id`, `status`, `due_date`, `system_prompt`, `dependencies`, `is_sequential`) are stored in the `metadata` JSON field. Same for queries (`question`, `answer`, `source`, `is_gap`).
- **Typed constructors/helpers per domain:** e.g. `CreateTask(...)`, `GetTask(...)`, `QueryLog{}`, `Entry{}` — these live in new files but return/accept typed wrappers around `KnowledgeNode`.
- **`pkg/journal/analysis.go` stays in `pkg/journal` last:** The `AnalyzeJournalEntry` / `JournalAnalysis` / `Entity` / `OpenLoop` types are used by `internal/agent/process_entry.go` and `internal/agent/graph_builder.go`. These move to `pkg/memory/analysis.go` as part of deleting `pkg/journal`.
- **`pending.go` migration:** Changes `PendingQuestionsCollection = "pending_questions"` → write to `journal` collection with `node_type = "pending_question"`. `TelegramQuestionStateCollection` stays as-is (separate concern).
- **Tasks collection → journal:** `findRecentDuplicateTask` query changes from `tasks` → `journal` filtered by `node_type == "task"`.
- **Query dedup:** `SaveQuery` moves from `queries` collection to `journal` with `node_type = "query"`.
- **No embedding for queries:** Queries don't currently have embeddings. Keep that — just store in journal with node_type=query.

---

## Edge Cases & Pre-Flight Checks

1. **Firestore indexes:** The `journal` collection already has composite indexes. Adding `node_type` filters to task/query queries requires new composite indexes (e.g. `node_type + timestamp`, `node_type + status + timestamp`, `node_type + is_gap + timestamp`). These must be added to `firestore.indexes.json`.
2. **`node_type` collision with `log`:** Existing code in `knowledge.go` already filters out `node_type == "log"` from knowledge searches. Need to also filter out `task`, `query`, `pending_question` from semantic knowledge searches — or rely on significance_weight filtering. Tasks should have a high significance weight; queries/pending_questions low. Best approach: in `QuerySimilarNodes` and `SearchKnowledgeNodes`, the existing `!= "log"` filter will include tasks/queries, so we need to decide if tasks should be searchable via semantic search (probably yes for task search) or excluded (probably yes for queries/pending_questions). We'll store tasks with `significance_weight = 0.7` and queries/pending_questions with `significance_weight = 0.1` so they're de-prioritized but present.
3. **`pkg/task/engine.go`:** Need to check what's in it and whether it moves cleanly.

---

## Affected Areas

- [x] Agent / FOH loop — `prompter.go` and `foh_helpers.go` import `pkg/task`; callers updated
- [x] Tools — `task_tools.go`, `journal_tools.go`, `query_tools.go` all import `pkg/journal` or `pkg/task`
- [x] Prompts / `app_capabilities.txt` — no new capabilities, but collection change is internal
- [x] Firestore schema or queries — new composite indexes needed on `journal` collection
- [x] API routes — `internal/api/handler_tasks.go` imports `pkg/task`
- [x] Memory / journal behavior — all three domain types moved to `journal` collection

---

## Open Questions

- [x] Does `KnowledgeNode` semantic search need to exclude `task`/`query`/`pending_question` node types? Decision: Keep tasks searchable (significance_weight=0.7); exclude query/pending_question from semantic memory searches via weight filter (they'll have significance_weight=0.1).

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [ ] LLM output parsed as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON)
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Firestore (if applicable)**
- [ ] Composite indexes defined in `firestore.indexes.json`
- [ ] `firebase deploy --only firestore:indexes` run (or `./scripts/deploy.sh`)

**Verification (Proof of Work)**
- [ ] **Compilation:** `go build ./...` passes cleanly.
- [ ] **Tests:** `go test ./...` passes.
- [ ] **Lint/Format:** Code is formatted and passes `go vet`.
- [ ] **Manual Smoke Test:** CLI `jot` command creates an entry and retrieves it; tasks still function.

**Wrap-up**
- [ ] `app_capabilities.txt` updated if capabilities changed
- [ ] `blueprint.md` consulted if core agentic loop was touched
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260319_memory-consolidation.md` (this file)
- `pkg/memory/schema.go` — add new node_types and task/query metadata types
- `pkg/memory/entry_nodes.go` — ported from `pkg/journal/entries.go`
- `pkg/memory/task_nodes.go` — ported from `pkg/task/tasks.go` + `schema.go` + `engine.go`
- `pkg/memory/query_nodes.go` — ported from `pkg/journal/queries.go`
- `pkg/memory/pending.go` — migrate to journal collection
- `pkg/memory/analysis.go` — ported from `pkg/journal/analysis.go`
- `internal/tools/impl/task_tools.go` — update imports
- `internal/tools/impl/journal_tools.go` — update imports
- `internal/tools/impl/query_tools.go` — update imports
- `internal/api/handler_tasks.go` — update imports
- `internal/agent/process_entry.go` — update imports
- `internal/agent/dreamer.go` — update imports
- `internal/agent/prompter.go` — update imports
- `firestore.indexes.json` — new indexes for task/query node types

---

## Session Log

_Most recent first._

<!-- 20260320 -->
- Phase 4 complete. Deleted `pkg/journal/` (8 files) and `pkg/task/` (3 files) — zero remaining callers confirmed before deletion. `go build ./...`, `go test ./...`, and `go vet ./...` all pass cleanly. Updated `firestore.indexes.json`: removed stale `tasks` vector index, `tasks` journal_entry_ids+timestamp index, and `queries` is_gap+timestamp index; added 4 new `journal` collection indexes for `node_type+journal_entry_ids+timestamp`, `node_type+is_gap+timestamp`, `node_type+created_at`, and `node_type+parent_id+timestamp`. Deployed indexes to Firebase project `journal-488418` (succeeded). Updated `blueprint.md` Memory Hierarchy table to reflect unified `journal` collection; removed stale `pkg/journal` reference from App/DI note. Updated `app_capabilities.txt` to document that queries and pending_questions are stored as `node_type: query` and `node_type: pending_question` in the `journal` collection, and that tasks use `pkg/memory`. Brief closed.

<!-- 20260319 -->
- Created brief and worktree (`../jot-memory-consolidation`). Writing implementation plan.
