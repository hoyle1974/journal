# Brief: Dead Code Pruning

**Date:** 20260326
**Status:** `done`
**Branch:** `feature/dead-code-pruning`
**Worktree:** `../jot-dead-code-pruning`

---

## Goal

Remove dead code, orphaned infrastructure, and unused utilities from the codebase. This includes 3 files to delete entirely and targeted function/constant removals from 6 files, reducing maintenance burden and improving clarity.

---

## Scope

**In:**
- Delete `internal/agent/decay_cron.go`, `internal/infra/deploy.go`, `pkg/utils/centroid.go`
- Remove dead functions/constants from `internal/infra/firestore.go`, `internal/infra/gemini.go`, `internal/infra/llm_metrics.go`, `pkg/utils/prompt.go`, `cmd/jot/main.go`, `internal/infra/obs.go`

**Out:**
- Any refactoring beyond the specified removals
- Changes to any other files unless required to fix a compile error caused by these removals

---

## Approach & Key Decisions

Surgical removal only. Delete specified symbols, verify nothing else references them, confirm build and tests pass.

---

## Edge Cases & Pre-Flight Checks

1. Some removed functions may be referenced transitively — check each before deleting.
2. `WrapFirestoreIndexError` must be preserved in `firestore.go` if it is used elsewhere.

---

## Affected Areas

- [ ] Agent / FOH loop — review `blueprint.md` before changing
- [ ] Tools — register via `tools.Register()` in `init()`, co-locate by domain
- [ ] Prompts / `app_capabilities.txt` — update if Jot's capabilities change
- [ ] Firestore schema or queries — update `firestore.indexes.json` if new composite indexes needed
- [ ] New dependencies / infra clients — pass via `*infra.App`, never hidden in context
- [ ] API routes or cron jobs
- [ ] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- None

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

**Verification (Proof of Work)**
- [ ] **Compilation:** `go build ./...` passes cleanly.
- [ ] **Tests:** `go test ./...` passes.
- [ ] **Lint/Format:** Code is formatted and passes `go vet`.

**Wrap-up**
- [ ] `app_capabilities.txt` updated if capabilities changed
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

briefs/active/20260326_dead-code-pruning.md (this file)
internal/agent/decay_cron.go
internal/infra/deploy.go
pkg/utils/centroid.go
internal/infra/firestore.go
internal/infra/gemini.go
internal/infra/llm_metrics.go
pkg/utils/prompt.go
cmd/jot/main.go
internal/infra/obs.go

---

## Session Log

<!-- 20260326 -->
- Completed all removals: deleted 3 files + 4 test files, removed 16 functions/constants across 6 files. `go build ./...` and `go test ./...` both pass cleanly.
