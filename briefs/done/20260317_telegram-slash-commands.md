# Brief: Telegram Slash Commands (/dream, /recall, /help)

**Date:** 20260317
**Status:** `in-progress`
**Branch:** `feature/telegram-slash-commands`
**Worktree:** `../jot-telegram-slash-commands`

---

## Goal

Add `/dream`, `/recall`, and `/help` slash commands to the Telegram bot. `/dream` triggers the dreamer pipeline and streams progress logs back to the chat. `/recall` fetches and sends the latest dream narrative. `/help` lists available commands.

---

## Scope

**In:**
- `/dream` — acquires dream run lock, starts dreamer in a goroutine, streams phase/log updates to Telegram via `telegramDreamerProgress`
- `/recall` — fetches `_system/latest_dream` and sends the narrative to the chat
- `/help` — sends a static list of available slash commands

**Out:**
- No new Firestore collections or indexes
- No changes to the FOH/agent loop
- `/skip` is unaffected (handled upstream in `telegram_process.go`)

---

## Approach & Key Decisions

- Intercept slash commands in `handleProcessTelegramQuery` (handler_tasks.go) **before** calling `ProcessIncomingTelegram`. This keeps the `s.Telegram` and `s.Agent` access clean.
- Unknown slash commands fall through to FOH.
- New file `handler_telegram_commands.go` holds the three slash command handlers + `telegramDreamerProgress`.
- `/dream` uses `TryAcquireDreamRunLock` to prevent concurrent runs (same as the HTTP `/dream` endpoint). Runs in a goroutine with a 55-min context derived from `s.App.WithContext(context.Background())`.
- `telegramDreamerProgress.OnLog` sends each log line as a Telegram message. `OnPhase` sends a "Phase: X" update.

---

## Edge Cases & Pre-Flight Checks

1. Concurrent `/dream` triggers — handled via `TryAcquireDreamRunLock`.
2. Dream takes a long time — goroutine uses its own long-lived context, not the HTTP request context.
3. Telegram rate limits — progress sends one message per log line; this is acceptable since the dreamer batches logs (every 5 facts or on last).

---

## Affected Areas

- [x] API routes — `handler_tasks.go` + new `handler_telegram_commands.go`
- [ ] Prompts / `app_capabilities.txt` — update to mention Telegram slash commands

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Verification (Proof of Work)**
- [ ] `go build ./...` passes cleanly
- [ ] `go vet ./...` passes
- [ ] `app_capabilities.txt` updated

---

## Key Files

- `briefs/active/20260317_telegram-slash-commands.md`
- `internal/api/handler_tasks.go`
- `internal/api/handler_telegram_commands.go` (new)
- `internal/api/backend.go`
- `internal/prompts/app_capabilities.txt`

---

## Session Log

<!-- 20260317 -->
- Session 1: Created brief and worktree; implementing /dream, /recall, /help slash commands in handler_telegram_commands.go with telegramDreamerProgress for streaming logs.
