# Brief: Graph Traversal & Graph RAG (Plan 1)

**Date:** 20260319
**Status:** `in-progress`
**Branch:** `feature/graph-traversal`
**Worktree:** `../jot-graph-traversal`

---

## Goal

Add 1-hop graph traversal to the knowledge graph so the FOH agent can explore relationships around any discovered node UUID. This enables Graph RAG: after a `semantic_search` returns a node, the agent can call `graph_expand` to retrieve its outgoing SPO edges, incoming entity_link references, and directly linked nodes — surfacing relational context that pure vector search misses.

---

## Scope

**In:**
- `pkg/memory/graph.go` — `GraphExpand` function + `GraphExpandResult` struct
- `internal/tools/impl/helpers.go` — `formatKnowledgeNodes` emits `   UUID: <id>` lines
- `internal/tools/impl/graph_tools.go` — `graph_expand` tool + `formatGraphExpandResult`
- `internal/tools/impl/memory_tools.go` — `registerGraphTools()` called from existing `init()`
- `internal/agent/foh.go` — `graph_expand` added to both `searchTools` maps

**Out:**
- Multi-hop traversal (future)
- `extractUUIDsFromSearchResult` helper (future)
- Prompt updates to `app_capabilities.txt`

---

## Approach & Key Decisions

- `GraphExpand` validates env/seedID before calling `StartSpan` to avoid panics in test context where tracer is not initialized.
- `formatKnowledgeNodes` in `helpers.go` now emits `   UUID: <id>` (3 spaces) so results from `semantic_search` and `list_knowledge` include parseable UUIDs that users/LLM can pass to `graph_expand`.
- `graph_expand` tool is registered via `registerGraphTools()` called from the single `init()` in `memory_tools.go`.
- Both `searchTools` maps in `foh.go` include `"graph_expand": true` so retrieved content is accumulated and evaluated for knowledge gaps.

---

## Edge Cases & Pre-Flight Checks

1. `StartSpan` panics if called before env validation in test context — resolved by checking `env == nil` first.
2. Firestore queries for `QueryNodesLinkingTo` and `QueryOutgoingEdges` require existing composite indexes — these were pre-existing.

---

## Affected Areas

- [x] Agent / FOH loop — `searchTools` maps updated
- [x] Tools — `graph_expand` registered via `tools.Register()` in `registerGraphTools()`
- [ ] Prompts / `app_capabilities.txt` — pending update
- [ ] Firestore schema or queries — uses existing indexes

---

## Checklist

**Implementation**
- [x] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Every significant agentic step has `StartSpan` / `defer span.End()`
- [x] Errors wrapped with `%w`, not `%v`
- [x] No file exceeds 400 lines

**Verification (Proof of Work)**
- [x] **Compilation:** `go build ./...` passes cleanly.
- [x] **Tests:** `go test ./...` passes — all 10 packages green.
- [x] **Lint/Format:** `go vet ./...` clean.

---

## Key Files

- `briefs/active/20260319_graph-traversal.md` (this file)
- `pkg/memory/graph.go`
- `pkg/memory/graph_test.go`
- `internal/tools/impl/graph_tools.go`
- `internal/tools/impl/helpers.go`
- `internal/tools/impl/memory_tools.go`
- `internal/agent/foh.go`

---

## Session Log

<!-- 20260319 -->
- Implemented Plan 1 (Graph Traversal & Graph RAG) in full: `GraphExpand` in `pkg/memory/graph.go`, UUID lines in `formatKnowledgeNodes`, `graph_expand` tool in `graph_tools.go`, `registerGraphTools()` wired into existing `init()`, both `searchTools` maps in `foh.go` updated. All tests pass (`go test ./...` green, `go vet ./...` clean).
