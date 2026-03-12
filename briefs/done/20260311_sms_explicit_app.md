# Brief: Fix infra.GetApp(ctx) in sms_process.go

**Date:** 20260311
**Status:** `done`
**Branch:** `feature/sms-explicit-app`
**Worktree:** `../journal-sms-explicit-app`

---

## Goal

`internal/service/sms_process.go` calls `infra.GetApp(ctx)` in both `processQuerySMS` and `processEntrySMS` — the exact pattern `.cursorrules` flags as legacy and prohibits in new code. SMS query processing is a hot path (every inbound SMS hits it), so hiding the app dependency in context is both a correctness risk and a violation of the project's explicit DI rules. Thread `app *infra.App` explicitly from the call site down through the call chain.

---

## Scope

**In:**
- `internal/service/sms_process.go`: remove all `infra.GetApp(ctx)` calls; receive `app *infra.App` explicitly in `ProcessIncomingSMS`, `processQuerySMS`, and `processEntrySMS`
- `internal/service/sms_service.go`: update `SMSService.ProcessIncomingSMS` to pass `app` down; `SMSService` already holds no `app` field — decide whether to add one or receive it from the call site
- `internal/api/handler_sms.go`: update call to `s.SMS.ProcessIncomingSMS` to pass `s.App` (already available on the server)
- `internal/api/handler_tasks.go`: update call to `s.SMS.ProcessIncomingSMS` in `handleProcessSMSQuery` to pass `s.App`
- `internal/api/backend.go`: update `APIBackend.ProcessIncomingSMS` wrapper if still present
- Assess `processEntrySMS`: it is never called from `ProcessIncomingSMS` — confirm it is dead code and delete it if so

**Out:**
- No behaviour changes to SMS processing logic
- No changes to Twilio signature validation, phone allowlist, or reply delivery
- No changes to the `SMSService` interface contract beyond the `app` parameter addition

---

## Approach & Key Decisions

The cleanest fix threads `app` through the existing call chain rather than adding a field to `SMSService`. `SMSService` is a thin adapter; adding an `app` field would give it a second responsibility (infrastructure ownership) that belongs to the `AgentService` layer. The preferred shape is:

```go
// sms_service.go
func (s *SMSService) ProcessIncomingSMS(ctx context.Context, app *infra.App, msg *infra.TwilioWebhookRequest) string

// sms_process.go
func ProcessIncomingSMS(ctx context.Context, app *infra.App, msg *infra.TwilioWebhookRequest) string
func processQuerySMS(ctx context.Context, app *infra.App, query, from string) string
func processEntrySMS(ctx context.Context, app *infra.App, text, from string) string  // or delete
```

Call sites already have `app` available:
- `handler_sms.go`: `s.App` on the `*Server`
- `handler_tasks.go`: `s.App` on the `*Server`
- The goroutine fallback in `handler_sms.go` uses `s.App.WithContext(context.Background())` — pass `s.App` directly alongside

The `api.SMSService` interface in `internal/api/backend.go` must be updated to match the new signature. Confirm the interface definition in `internal/api/backend.go` and update it before updating implementations so the compiler catches all stale call sites.

`processEntrySMS` is currently dead — `ProcessIncomingSMS` routes everything through `processQuerySMS`. Delete it unless there is an intentional plan to restore the log-only path; keeping dead code with a `GetApp` call is pure risk.

---

## Affected Areas

- [ ] Agent / FOH loop — not touched
- [ ] Tools — not touched
- [ ] Prompts / `app_capabilities.txt` — no capability change
- [ ] Firestore schema or queries — no changes
- [x] New dependencies / infra clients — `SMSService` call sites gain an `app` parameter; no new clients
- [ ] API routes or cron jobs — no changes
- [ ] Memory / journal behavior — no changes

---

## Open Questions

- [ ] Should `processEntrySMS` be deleted or kept with a comment explaining the intended future use? Default: delete.
- [ ] Does the `api.SMSService` interface need a version bump comment or is the compiler error at build time sufficient signal for reviewers?

---

## Checklist

**Implementation**
- [x] `infra.GetApp(ctx)` does not appear anywhere in `sms_process.go` after the change
- [x] `ProcessIncomingSMS`, `processQuerySMS` receive `app *infra.App` explicitly
- [x] `processEntrySMS` deleted (or retained with explicit justification in Session Log)
- [x] `SMSService.ProcessIncomingSMS` signature updated in `sms_service.go`
- [x] `api.SMSService` interface updated in `internal/api/backend.go`
- [x] `handler_sms.go` passes `s.App` to `ProcessIncomingSMS` — including in the goroutine fallback
- [x] `handler_tasks.go` passes `s.App` to `ProcessIncomingSMS`
- [x] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Errors wrapped with `%w`, not `%v`
- [x] No file exceeds 400 lines

**Firestore (if applicable)**
- [ ] N/A — no index changes

**Wrap-up**
- [x] `app_capabilities.txt` — no update needed
- [x] `blueprint.md` — not touched
- [x] `go build ./...` passes
- [x] `go test ./...` passes
- [x] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/done/20260311_sms_explicit_app.md` (this file)
- `internal/service/sms_process.go` — primary change; remove `GetApp(ctx)` calls
- `internal/service/sms_service.go` — update `ProcessIncomingSMS` signature
- `internal/api/backend.go` — update `SMSService` interface and `APIBackend` wrapper
- `internal/api/handler_sms.go` — update call site, including goroutine fallback
- `internal/api/handler_tasks.go` — update call site in `handleProcessSMSQuery`

---

## Session Log

<!-- Most recent first -->

<!-- 20260311 (session 2) -->
- Worktree `../journal-sms-explicit-app` created (branch `feature/sms-explicit-app`). `sms_process.go` already had explicit `app` and no `GetApp(ctx)`; `processEntrySMS` already removed. Implemented preferred shape: removed `app` field from `SMSService`; `ProcessIncomingSMS(ctx, app *infra.App, msg)` now takes `app` at call site. Updated `api.SMSService` interface in `backend.go`; `sms_service.go` now `NewSMSService(getConfig)` only and `ProcessIncomingSMS(ctx, app, msg)` forwards to `service.ProcessIncomingSMS`. Handlers pass `s.App.(*infra.App)` (Server holds `AppLike`). `function.go` calls `NewSMSService(getConfig)`. `go build ./...` and `go test ./...` pass.

<!-- 20260311 -->
- Brief created. Two live `infra.GetApp(ctx)` calls confirmed in `sms_process.go`. `processEntrySMS` identified as dead code — default decision is to delete. Interface update in `api.SMSService` is the load-bearing change that will surface all stale call sites at compile time.
