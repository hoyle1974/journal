# Brief: Telegram Question Flow

**Date:** 20260318
**Status:** `in-progress`
**Branch:** `feature/telegram-question-flow`
**Worktree:** `../jot-telegram-question-flow`

---

## Goal

Allow the pending-questions system (onboarding questions, Dreamer gap questions) to work through the Telegram bot interface. When the user messages the bot and there are unresolved pending questions, the bot asks the first question instead of running the FOH query. Each reply resolves that question and either asks the next one or resumes normal FOH processing.

---

## Scope

**In:**
- Stateful "active question" tracking per Telegram chat_id in a new Firestore collection `telegram_question_state`
- `pkg/memory/pending.go`: three new functions (Get/Set/Clear TelegramActiveQuestion)
- `internal/service/telegram_process.go`: intercept incoming messages to check/answer questions
- `/skip` support to skip a question without answering
- `app_capabilities.txt` update

**Out:**
- SMS question flow (separate concern)
- UI for listing all pending questions via Telegram command
- Changes to CLI or API question flow

---

## Approach & Key Decisions

**State**: Store `telegram_question_state/{chat_id}` document with `question_uuid` and `set_at` fields. This lets us track which question we asked and match the next incoming reply as the answer.

**Interception logic** (in `ProcessIncomingTelegram`):
1. If chat has an active question in Firestore:
   - If message is "skip" or "/skip": clear state, ask next question or run FOH
   - Otherwise: resolve active question with the message as answer, ask next question or run FOH
2. Else if unresolved pending questions exist: ask the oldest one, set it as active, return (don't run FOH)
3. Else: run FOH normally

**UX**: Questions are prefixed with context. After the last question is answered, run FOH on the original message so the user gets a response too.

**Skip**: `/skip` skips the current question without storing an answer.

---

## Edge Cases & Pre-Flight Checks

1. **Race condition**: Two Telegram messages arrive simultaneously for the same chat_id. The Set/Clear operations are single Firestore doc writes; last-write-wins is acceptable for a single-user scenario.
2. **Stale active question**: If the question was already resolved by another client (CLI), `GetTelegramActiveQuestion` should return nil so the bot doesn't try to re-resolve. Fetch the question from Firestore and check `resolved_at` before resolving.
3. **Empty body with image**: Image messages should skip the question flow and go straight to image processing. Handle in the webhook handler, not in `ProcessIncomingTelegram`.

---

## Affected Areas

- [x] Firestore schema or queries — new `telegram_question_state` collection (no composite indexes needed, single-doc lookups only)
- [x] Prompts / `app_capabilities.txt` — Telegram question flow is a new capability
- [x] API routes or cron jobs — no new routes

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Firestore**
- [ ] No composite indexes needed (simple doc Get/Set)

**Verification**
- [ ] `go build ./...` passes cleanly
- [ ] `go vet ./...` passes
- [ ] Manual: send first Telegram message → bot asks onboarding question → reply → bot asks next → all done → FOH runs normally

**Wrap-up**
- [ ] `app_capabilities.txt` updated
- [ ] Brief moved to `briefs/done/`

---

## Key Files

briefs/active/20260318_telegram-question-flow.md
pkg/memory/pending.go
internal/service/telegram_process.go
internal/api/handler_tasks.go

---

## Session Log

<!-- 20260318 -->
- Initial session: brief created, worktree pending.
