# Brief: System Service DI & Cron Abstraction (Phases 2 & 3)

**Date:** 20260314
**Status:** `done`
**Branch:** `feature/system-di-and-cron-abstraction`
**Worktree:** (removed)

---

## Goal

Complete the eradication of Firestore leaks across the architecture. First, by injecting a `SystemService` interface into the API router so handlers don't depend on passing the raw `App` object into `pkg/system`. Second, by extracting the raw Firestore iterators out of the background cron services (`internal/service/cron.go`) and pushing them down into the `pkg/memory` domain.

---

## Scope

**In:**
- **Phase 2 (API DI):**
  - Define a `SystemService` interface in `internal/api/backend.go` encompassing the methods created in `pkg/system` (e.g., lock management, dream state, debounce, onboarding).
  - Update the `Server` struct in `internal/api/server.go` and `NewServer` constructor to accept `System SystemService`.
  - Create `internal/service/system_service.go` to wrap `pkg/system` and satisfy the new API interface.
  - Refactor `handler_cron.go` and `handler_gdoc.go` to call `s.System.*` rather than calling `system.*` functions with `s.App`.
- **Phase 3 (Cron Abstraction):**
  - Refactor `RunJanitor` in `internal/service/cron.go` to remove raw `client.Collection(...).Where(...)` calls. Move this data access logic to a new method like `memory.EvictStaleNodes(ctx, threshold, staleDays)`.
  - Refactor `RunPulseAudit` in `internal/service/cron.go` to use a new method like `memory.GetNodesForPulseAudit(ctx, threshold, staleDays)`.
  - Remove all `cloud.google.com/go/firestore` and `google.golang.org/api/iterator` imports from `internal/service/cron.go`.

**Out:**
- Modifying core FOH logic or LLM prompts.
- Changing how data is actually structured in Firestore.

---

## Approach & Key Decisions

We are formalizing the boundaries. The API layer should only know about interfaces defined in `internal/api/backend.go`. By creating `SystemService`, we mock-proof the system state handlers. For the cron jobs, `internal/service/cron.go` acts as the orchestrator, but the actual database querying and iteration belongs in `pkg/memory`, maintaining the rule that only `pkg/` domains handle raw data access.

---

## Edge Cases & Pre-Flight Checks
1. **Server Initialization:** Updating `NewServer` requires updating the `init()` function in `function.go` and `testServerForAPI` in `internal/api/auth_test.go` and `server_test.go` to inject the new `SystemService`.
2. **Iterator Contexts:** When moving the Janitor and Pulse Audit iterators to `pkg/memory`, ensure the batch processing/deletion logic still correctly logs its progress and handles `iterator.Done` without swallowing unexpected Firestore errors.

---

## Affected Areas

- [ ] Agent / FOH loop
- [ ] Tools
- [ ] Prompts / `app_capabilities.txt`
- [ ] Firestore schema or queries
- [x] New dependencies / infra clients — passing `SystemService` into the API.
- [x] API routes or cron jobs — `internal/service/cron.go`, `internal/api/server.go`, `internal/api/handler_*.go`.
- [ ] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- [ ] ...

---

## Checklist

**Implementation**
- [x] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Debug logs pass full strings — no truncation at Debug level
- [x] Every significant agentic step has `StartSpan` / `defer span.End()`
- [x] Errors wrapped with `%w`, not `%v`
- [x] No file exceeds 400 lines

**Verification (Proof of Work)**
- [x] **Compilation:** `go build ./...` passes cleanly.
- [x] **Tests:** `go test ./...` passes.
- [x] **Lint/Format:** Code is formatted and passes `go vet`.
- [ ] **Manual Smoke Test:** Trigger the janitor via CLI (`jot janitor`) to ensure the newly abstracted memory functions execute successfully (optional).

**Wrap-up**
- [x] `app_capabilities.txt` updated if capabilities changed — N/A (no capability change)
- [x] `blueprint.md` consulted if core agentic loop was touched — N/A
- [x] Tests added / updated — existing tests pass; testServerForAPI and server_test updated for new System param
- [x] Brief status set to `done` and file moved to `briefs/done/`

---

Key Files
- briefs/done/20260314_system-di-and-cron-abstraction.md
- internal/api/backend.go
- internal/api/server.go
- internal/api/handler_cron.go
- internal/api/handler_gdoc.go
- internal/service/system_service.go
- internal/service/cron.go
- pkg/memory/janitor.go
- function.go

---

## Session Log
- Created brief to finish Firestore encapsulation (API DI and Cron abstraction).
- **2026-03-14:** Phase 2: Added `SystemService` interface in `internal/api/backend.go` (sync, latest dream, dream run, onboarding). Added `System SystemService` to `Server` and `NewServer(..., system, router)`. Created `internal/service/system_service.go` wrapping `pkg/system` with `*infra.App`. Refactored `handler_cron.go` and `handler_gdoc.go` to use `s.System.*`; `dreamRunProgress` now takes `SystemService` and `runID`. Phase 3: Added `pkg/memory/janitor.go` with `EvictStaleNodes(ctx, weightThreshold, staleDays)` and `CreatePulseAuditSignals(ctx, importanceThreshold, staleDays)` (returns `PulseAuditResult`). Refactored `RunJanitor` and `RunPulseAudit` in `internal/service/cron.go` to call these; removed `cloud.google.com/go/firestore` and `google.golang.org/api/iterator` from cron.go. Wired `systemSvc := service.NewSystemService(app)` in `function.go` and added `nil` for System in `auth_test.go` and `server_test.go`. `go build ./...` and `go test ./...` pass.
- **2026-03-14:** Committed on feature branch; merged `feature/system-di-and-cron-abstraction` into main; removed worktree. Brief moved to `briefs/done/`.
