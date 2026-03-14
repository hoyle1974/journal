# Brief: System State Abstraction

**Date:** 20260314
**Status:** `done`
**Branch:** `feature/system-state-abstraction`
**Worktree:** (removed)

---

## Goal

Encapsulate all Firestore `_system` collection interactions into a dedicated `pkg/system` package. This stops database implementation details from bleeding into the API (`internal/api`), Service (`internal/service`), and Tool (`internal/tools`) layers, improving testability and domain separation.

---

## Scope

**In:**
- Create a new `pkg/system` package.
- Move `sync_lock`, `sync_state`, and `sync_debounce` logic from `internal/api/handler_gdoc.go` to `pkg/system`.
- Move `latest_dream` logic from `internal/api/handler_cron.go` and `internal/tools/impl/dream_tools.go` to `pkg/system`.
- Move `dream_run_state.go` logic to `pkg/system`.
- Move `_system/onboarding` logic from `internal/service/onboarding.go` to `pkg/system`.
- Remove `cloud.google.com/go/firestore` imports from the refactored files.

**Out:**
- Phase 2 (Dependency Injection of the new system service into the API router).
- Phase 3 (Moving `Janitor` and `PulseAudit` Firestore queries out of `cron.go` into `pkg/memory`).
- Modifying core `entries` or `knowledge_nodes` data models.

---

## Approach & Key Decisions

We will create a centralized `pkg/system` package responsible for all single-document state configuration (locks, runs, debounce, onboarding).
Functions in `pkg/system` accept `FirestoreProvider` (implemented by `*infra.App` and `api.AppLike`) and `context.Context`. Transaction logic (e.g. `TryAcquireDreamRunLock`, `AcquireSyncLock`) was migrated intact to preserve atomicity.

---

## Edge Cases & Pre-Flight Checks
1. **Transaction Atomicity:** Moving `acquireSyncLock` and `TryAcquireDreamRunLock` out of the API package means ensuring the Firestore transaction closures still behave correctly and don't introduce race conditions.
2. **Circular Dependencies:** Ensure `pkg/system` does not accidentally import `pkg/agent` or `internal/api` which would cause cycles. It should rely mostly on `pkg/infra`.
3. **Cloud Tasks Payload:** `handler_gdoc.go` uses UUIDs and timestamps for debounce state. Ensure the abstraction in `pkg/system` supports passing/returning these cleanly.

---

## Affected Areas

- [ ] Agent / FOH loop
- [x] Tools — `get_latest_dream` in `dream_tools.go`
- [ ] Prompts / `app_capabilities.txt`
- [ ] Firestore schema or queries
- [x] New dependencies / infra clients — centralizing in `pkg/system`
- [x] API routes or cron jobs — `handler_gdoc.go`, `handler_cron.go`, `dream_run_state.go`
- [ ] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- [x] Should `pkg/system` accept `*infra.App` directly, or specific `*firestore.Client` instances? Resolved: use `FirestoreProvider` interface so both `*infra.App` and `api.AppLike` work.

---

## Checklist

**Implementation**
- [x] New code passes `*infra.App` or `FirestoreProvider` explicitly — no `infra.GetApp(ctx)` in new pkg/system code (only in `GetLatestDreamFromContext` for legacy tools)
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Debug logs pass full strings — no truncation at Debug level
- [x] Every significant agentic step has `StartSpan` / `defer span.End()` (existing handlers unchanged)
- [x] Errors wrapped with `%w`, not `%v`
- [x] No file exceeds 400 lines

**Verification (Proof of Work)**
- [x] **Compilation:** `go build ./...` passes cleanly.
- [x] **Tests:** `go test ./...` passes.
- [x] **Lint/Format:** Code formatted and passes `go vet`.
- [ ] **Manual Smoke Test:** Trigger a sync or dream via CLI to verify locks and state documents still function (optional).

**Wrap-up**
- [x] `app_capabilities.txt` updated if capabilities changed — N/A (no capability change)
- [x] `blueprint.md` consulted if core agentic loop was touched — N/A
- [x] Tests added / updated — existing tests pass
- [x] Brief status set to `done` and file moved to `briefs/done/`

---

Key Files
- briefs/done/20260314_system-state-abstraction.md
- internal/api/handler_gdoc.go
- internal/api/handler_cron.go
- internal/tools/impl/dream_tools.go
- internal/service/onboarding.go
- pkg/system/app.go
- pkg/system/sync.go
- pkg/system/latest_dream.go
- pkg/system/dream_run.go
- pkg/system/onboarding.go

---

## Session Log
- Created brief to abstract _system Firestore collection.
- **2026-03-14:** Implemented pkg/system: added sync.go (AcquireSyncLock, ReleaseSyncLock, GetSyncStateLastBlockHash, SetSyncStateAfterProcess, GetDebounceState, SetDebounceState), latest_dream.go (GetLatestDream, MarkLatestDreamRead, WriteLatestDream, GetLatestDreamFromContext), dream_run.go (DreamRunState, GetDreamRunState, TryAcquireDreamRunLock, UpdateDreamRunPhase, SetDreamRunCompleted, SetDreamRunFailed, AppendDreamRunLog), onboarding.go (OnboardingDocExists, SetOnboardingComplete), app.go (FirestoreProvider interface). Refactored handler_gdoc.go, handler_cron.go to use pkg/system; removed dream_run_state.go; updated dream_tools.go, dreamer.go, onboarding.go to use pkg/system. All functions take FirestoreProvider (or *infra.App where app is from context). Removed firestore imports from handler_gdoc, handler_cron, dream_tools, onboarding, dreamer. go build ./... and go test ./... pass.
- **2026-03-14:** Merged feature/system-state-abstraction into main; removed worktree; moved brief to briefs/done/.
