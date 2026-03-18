# Brief: Unified Single-Table Journal Architecture (Gold vs. Gravel Routing)

**Date:** 20260317
**Status:** `in-progress`
**Branch:** `feature/unified-journal-table`
**Worktree:** `../jot-unified-journal-table`

---

## Goal

Consolidate the `entries` (Episodic Memory) and `knowledge_nodes` (Semantic Memory) collections into a single, unified `journal` collection in Firestore. Simplify the retrieval architecture by replacing parallel Reciprocal Rank Fusion (RRF) with targeted, single-pass vector searches routed by a `significance_weight` threshold.

---

## Scope

**In:**
- Rename collection targets in `pkg/journal` and `pkg/memory` to point to a unified `journal` collection.
- Update `agent.ProcessEntry` to write raw logs with `node_type: "log"` and store their `significance_weight` and `embedding` in the same document.
- Rewrite `semantic_search` tool to perform a single `FindNearest` query using a `.Where("significance_weight", ">=", 0.7)` pre-filter, eliminating the 4-way parallel RRF logic.
- Rewrite `search_entries`, `get_recent_entries`, and `get_oldest_entries` tools to filter strictly by `node_type == "log"`.
- Update `RunJanitor` to explicitly exclude `node_type == "log"` from garbage collection to prevent accidental deletion of raw chronological history.
- Add required composite vector indexes to `firestore.indexes.json`.
- Create a one-off Go migration script (`cmd/admin/migrate_to_single_table.go`) to copy existing `entries` and `knowledge_nodes` into the new `journal` collection.

**Out:**
- Changes to the LLM models or prompt phrasing (beyond updating `app_capabilities.txt` to reflect tool changes).
- Changes to the Dreamer's extraction logic (it will just read/write to the new table).
- Changes to `tasks` or `_system` collections.

---

## Approach & Key Decisions

**1. The Unified Schema (`journal` collection)**
Every document in the system will now share a base schema.
- **Episodic Logs:** `node_type: "log"`, `source: "<cli|sms|etc>"`, `significance_weight: <float>`, `embedding: <vector>`.
- **Semantic Facts:** `node_type: "<person|project|fact|preference>"`, `significance_weight: <float>`, `embedding: <vector>`, `metadata: <json>`.

**2. Query Routing via Significance (The Gatekeeper)**
We maintain the "Gold vs. Gravel" philosophy at the query level rather than the table level.
- `semantic_search` (looking for facts) will query: `Collection("journal").Where("significance_weight", ">=", 0.7).FindNearest(...)`
- `search_entries` (looking for past events) will query: `Collection("journal").Where("node_type", "==", "log").FindNearest(...)`

**3. Janitor Protection**
The Janitor currently sweeps `knowledge_nodes` for low-significance items. It MUST be updated to query `Where("node_type", "not-in", []string{"log", "identity_anchor", "user_identity"})` to ensure raw episodic logs are never decayed or deleted.

**4. Eradicating RRF Boilerplate**
`internal/tools/impl/memory_tools.go` currently uses WaitGroups to hit both tables with both keyword and vector searches. This will be stripped down to a single Firestore `FindNearest` call, significantly reducing latency and code surface area.

---

## Edge Cases & Pre-Flight Checks

1.  **Firestore Composite Vector Indexing:** Firestore strictly requires a composite index when combining a `.Where()` filter with `.FindNearest()`. We must define `{ "fieldPath": "significance_weight", "order": "DESCENDING" }` alongside the vector configuration in `firestore.indexes.json`, as well as an index for `node_type`. If missed, queries will fail with `FailedPrecondition`.
2.  **Janitor Catastrophe:** If the Janitor logic is flawed, it will permanently delete the user's raw journal history. The exclusion of `node_type == "log"` must be unit-tested or rigorously smoke-tested before merging.
3.  **Migration Idempotency:** The migration script must gracefully handle being interrupted and restarted without duplicating records.

---

## Affected Areas

- [x] Agent / FOH loop — Simplifies retrieval latency.
- [x] Tools — `semantic_search`, `search_entries`, `get_recent_entries`, `get_oldest_entries` in `memory_tools.go` and `journal_tools.go`.
- [x] Prompts / `app_capabilities.txt` — Update tool descriptions if their parameters or behaviors shift slightly.
- [x] Firestore schema or queries — Unification of collections; `firestore.indexes.json` requires new vector composite indexes.
- [ ] New dependencies / infra clients
- [x] API routes or cron jobs — Janitor logic (`pkg/memory/janitor.go`) must be updated.
- [x] Memory / journal behavior (Gold vs Gravel semantics) — Handled via field-level routing rather than collection isolation.

---

## Open Questions

- [ ] Will we drop keyword fallback entirely for `semantic_search`, or maintain a secondary keyword query on the unified table for exact-match safety? *(Decision: Keep a single keyword fallback query on the unified table, fused locally, but drop the cross-table complexity).*

---

## Checklist

**Implementation**
- [ ] Create `cmd/admin/migrate_single_table/main.go` to copy `entries` and `knowledge_nodes` to `journal`.
- [ ] Update `EntriesCollection` and `KnowledgeCollection` constants to `"journal"` (or consolidate them into a single constant).
- [ ] Refactor `agent.ProcessEntry` to ensure `node_type: "log"` and `significance_weight` are saved together with the embedding.
- [ ] Refactor `semantic_search` in `internal/tools/impl/memory_tools.go` to use the `significance_weight >= 0.7` filter. Remove the 4-way WaitGroup logic.
- [ ] Refactor `search_entries` in `internal/tools/impl/journal_tools.go` to use `node_type == "log"`.
- [ ] Refactor `RunJanitor` in `pkg/memory/janitor.go` to explicitly bypass `node_type == "log"`.
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)`.
- [ ] All logging uses `LoggerFrom(ctx)`.
- [ ] Debug logs pass full strings — no truncation at Debug level.

**Firestore (if applicable)**
- [ ] Composite indexes defined in `firestore.indexes.json` for `significance_weight` (ASC/DESC) + `embedding` (VECTOR), and `node_type` (ASC) + `embedding` (VECTOR).
- [ ] `firebase deploy --only firestore:indexes` run (or `./scripts/deploy.sh`).

**Verification (Proof of Work)**
- [ ] **Compilation:** `go build ./...` passes cleanly.
- [ ] **Tests:** `go test ./...` passes.
- [ ] **Lint/Format:** Code is formatted and passes `go vet`.
- [ ] **Manual Smoke Test:** Log a new entry, trigger a dream run, and execute a `semantic_search` and a `search_entries` query via the CLI to verify routing isolation.

**Wrap-up**
- [ ] `app_capabilities.txt` updated to reflect the streamlined retrieval architecture.
- [ ] Brief status set to `done` and file moved to `briefs/done/`.

---

## Key Files

`briefs/active/20260317_unified-journal-table.md`
`pkg/journal/entries.go`
`pkg/memory/knowledge.go`
`pkg/memory/janitor.go`
`internal/tools/impl/memory_tools.go`
`internal/tools/impl/journal_tools.go`
`internal/agent/process_entry.go`
`firestore.indexes.json`

---

## Session Log

- 20260317: Brief created and initialized for unified table migration.
