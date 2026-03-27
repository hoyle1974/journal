# Brief: Dead Code Cleanup

**Date:** 20260326
**Status:** `in-progress`
**Branch:** `feature/dead-code-cleanup`
**Worktree:** `../jot-dead-code-cleanup`

---

## Goal

Remove genuinely dead and redundant code identified by static analysis: unused functions/variables, and duplicate API structs where the architectural boundary is already breached.

---

## Scope

**In:**
- Remove `withStatus`/`statusResponse` + dispatch in `wrapAPI` (no handler ever calls `withStatus`)
- Remove `searchToolCallCount` variable in `foh.go` (incremented but never read)
- Remove `withCurrentEntryUUID` unexported wrapper in `foh.go` (trivially delegates to exported version)
- Remove `ParamNamesFromArgs` in `tools/schema.go` (never called)
- Remove `api.KnowledgeNode` in `backend.go` (defined but never referenced)
- Collapse `api.QueryResult` → use `agent.QueryResult` directly (identical structs + JSON tags; `api` already imports `agent`)
- Remove `queryResultToAPI` mapping function
- Collapse `api.DreamResult` → use `agent.DreamResult` directly (same fields; add JSON tags to agent type)

**Out:**
- `api.Entry` — intentionally filters `ParsedImageDescription`, `AudioURL`, `Transcription` from `memory.Entry`. Keep.
- `api.PendingQuestion` — intentionally filters `Answer`, `ResolvedAt`, `AskCount`, etc. Keep.
- Logging abstraction for the entry/exit sandwich in service files (separate concern, non-trivial)

---

## Approach & Key Decisions

- `api.QueryResult` and `agent.QueryResult` have identical fields and JSON tags. The `api` package already imports `internal/agent`. Collapsing them means updating the `AgentService` interface return types and removing `queryResultToAPI`.
- `api.DreamResult` has the same fields as `agent.DreamResult` but the agent type lacks JSON tags (snake_case). We add JSON tags to `agent.DreamResult` so the API response stays unchanged.
- `api.Entry` and `api.PendingQuestion` are intentional field filters (hide internal fields from the HTTP API). Keeping them is the correct call.

---

## Edge Cases & Pre-Flight Checks

1. `wrapAPI` has a type assertion on `*statusResponse` — removing it is safe only if no future handler ever uses the value. Verified: no call site exists.
2. `agent.DreamResult` adding JSON tags is a public type change — check if any other consumers depend on its current serialization (no JSON marshaling in agent tests).

---

## Affected Areas

- [x] Agent / FOH loop — `foh.go` (removing dead vars/wrapper only, not logic)
- [ ] Tools — `tools/schema.go` (removing dead `ParamNamesFromArgs`)
- [ ] API routes or cron jobs — `api_handler.go`, `backend.go`
- [ ] New dependencies / infra clients

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Verification (Proof of Work)**
- [x] **Compilation:** `go build ./...` passes cleanly.
- [x] **Tests:** `go test ./...` — 150 passed, 1 pre-existing failure (`CosineSimilarity` undefined in `pkg/utils`, also failing on main).
- [x] **Lint/Format:** `go vet ./...` — same pre-existing failure only.

**Wrap-up**
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

briefs/active/20260326_dead-code-cleanup.md
internal/api/api_handler.go
internal/api/backend.go
internal/agent/foh.go
internal/agent/dreamer.go
internal/service/agent_service.go
tools/schema.go

---

## Session Log

<!-- 20260326 -->
- Session start: created brief, full analysis complete. Identified 8 removals/collapses. Starting implementation.
