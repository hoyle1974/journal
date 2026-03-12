# Brief: Remove Legacy APIBackend from internal/service/backend.go

**Date:** 20260311
**Status:** `done`
**Branch:** `feature/remove-api-backend`
**Worktree:** `../journal-remove-api-backend`

---

## Goal

`internal/service/backend.go` is a ~280-line `APIBackend` struct that duplicates logic already handled by the four focused service types (`AgentService`, `JournalService`, `MemoryService`, `SMSService`). It predates the service split and should be deleted. Removing it eliminates a maintenance hazard where the same operation has two parallel implementations that can drift apart.

---

## Scope

**In:**
- Delete `internal/service/backend.go` entirely
- Delete `internal/service/backend.go`'s `APIBackend` struct and `NewAPIBackend` constructor
- Confirm no call sites reference `NewAPIBackend` or `APIBackend` directly (expected: none after the service split)
- Fix the two remaining `infra.GetApp(ctx)` calls in `internal/service/sms_process.go` (`processQuerySMS`, `processEntrySMS`) — pass `app` explicitly instead

**Out:**
- No changes to `AgentService`, `JournalService`, `MemoryService`, or `SMSService` behaviour
- No changes to the HTTP handlers or router
- No new features

---

## Approach & Key Decisions

`APIBackend` was an earlier monolithic implementation of `api.Backend` before the four service types were introduced. After the split, `function.go` wires the server using the focused services directly:

```go
journalSvc := service.NewJournalService()
memorySvc  := service.NewMemoryService()
agentSvc   := service.NewAgentService(app)
smsSvc     := service.NewSMSService(getConfig)
defaultServer = api.NewServer(app, defaultConfig, ..., journalSvc, memorySvc, agentSvc, smsSvc, api.Router)
```

`APIBackend` is no longer referenced at the entry points. The file is dead code. The safe removal path is:

1. `grep -r "APIBackend\|NewAPIBackend" .` to confirm no live call sites.
2. Delete `internal/service/backend.go`.
3. Fix `sms_process.go`: `processQuerySMS` and `processEntrySMS` both call `infra.GetApp(ctx)`. Instead, thread `app *infra.App` as a parameter from `ProcessIncomingSMS` downward, which already has access to it via the `SMSService` or can receive it from the call site in `handler_sms.go` / `handler_tasks.go`.
4. Run `go build ./...` and `go test ./...` to confirm clean.

The `sms_process.go` fix is included here because it is the last `infra.GetApp(ctx)` usage in non-legacy code and is directly related — both are tech-debt cleanup in the same `service` package.

---

## Affected Areas

- [x] Tools — no changes
- [ ] Agent / FOH loop — not touched
- [ ] Prompts / `app_capabilities.txt` — no capability change
- [ ] Firestore schema or queries — no changes
- [x] New dependencies / infra clients — removing one; `sms_process.go` will receive `app` explicitly
- [ ] API routes or cron jobs — no changes
- [ ] Memory / journal behavior — no changes

---

## Open Questions

- [ ] Confirm `APIBackend` has zero live call sites (grep before deleting).
- [ ] Decide whether `processEntrySMS` (currently unreachable dead code — all SMS is routed through `processQuerySMS`) should also be deleted or kept. Lean toward deleting it; it is not wired into `ProcessIncomingSMS`.

---

## Checklist

**Implementation**
- [x] `grep -r "APIBackend\|NewAPIBackend" .` returns no results outside `backend.go` itself
- [x] `internal/service/backend.go` deleted
- [x] `sms_process.go`: `ProcessIncomingSMS` receives `app *infra.App`; `processQuerySMS` receives and passes it explicitly — no `infra.GetApp(ctx)`
- [x] `processEntrySMS` deleted or confirmed intentionally kept (dead code) — deleted
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Errors wrapped with `%w`, not `%v`
- [x] No file exceeds 400 lines after edits

**Firestore (if applicable)**
- [x] N/A — no index changes

**Wrap-up**
- [x] `app_capabilities.txt` — no update needed (no capability change)
- [x] `blueprint.md` — not touched (no agentic loop change)
- [x] `go build ./...` passes
- [x] `go test ./...` passes
- [x] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260311_remove-api-backend.md` (this file)
- `internal/service/backend.go` — file to delete
- `internal/service/sms_process.go` — fix `infra.GetApp(ctx)` calls
- `internal/service/sms_service.go` — may need signature update for `ProcessIncomingSMS`
- `internal/api/handler_sms.go` — call site for `ProcessIncomingSMS`
- `internal/api/handler_tasks.go` — call site for `ProcessIncomingSMS` (via Cloud Task)
- `function.go` — confirm `NewAPIBackend` is not called here

---

## Session Log

<!-- Most recent first -->

<!-- 20260311 -->
- Executed: Grep confirmed no live call sites for `APIBackend`/`NewAPIBackend` outside `backend.go`. Deleted `internal/service/backend.go`. Moved `ConfigGetter` into `sms_service.go`. Threaded `app *infra.App` through `SMSService` (NewSMSService(getConfig, app)), `ProcessIncomingSMS(ctx, app, msg)`, and `processQuerySMS(ctx, app, query, from)`; removed both `infra.GetApp(ctx)` usages. Deleted dead `processEntrySMS`. Updated `function.go` to pass `app` to `NewSMSService`. `go build ./...` and `go test ./...` pass.
- Brief created. Analysis confirmed `APIBackend` is dead code post-service-split; `sms_process.go` `GetApp(ctx)` calls are the only remaining live tech debt in scope. No behaviour changes intended.
