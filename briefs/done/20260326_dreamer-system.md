# Brief: Dreamer System (Cognitive Background Loop)

**Date:** 20260326
**Status:** `in-progress`
**Branch:** `feature/dreamer-system`
**Worktree:** `../jot-dreamer-system`

---

## Goal

Implement a background "dream cycle" that periodically synthesizes recent log entries into narrative summary nodes and proactively identifies knowledge gaps (unknown people/projects) and stalled tasks. This transforms JOT from a reactive database into a proactive cognitive agent — one that already knows what happened because it synthesized that data while "sleeping."

---

## Scope

**In:**
- `_system/dream_meta` document for watermark state (last_processed_at, version)
- New `NodeTypeSummary = "summary"` node type in `memory/schema.go` (remove unused `NodeTypeWeeklySummary`)
- `internal/agent/dreamer.go` — core `RunDreamCycle(ctx, app, force)` function
- `POST /internal/dream` API endpoint
- `jot dream` CLI subcommand (force=true)
- `/dream` Telegram slash command
- `AgentService.RunDreamer` method wired through service/api layers
- `## 🧠 RECENT REFLECTIONS` block injected into FOH system prompt (3 most recent summaries)
- Cloud Scheduler job in `setup-infra.sh` + idempotent upsert on every `deploy.sh`
- Firestore index for `node_type=summary + timestamp DESC` (already covered by existing index)

**Out:**
- Level-2 (weekly) summary aggregation (future work)
- Embedding/semantic dedup for summary nodes (not needed for single-LLM summaries)
- New Telegram active-question flow changes (dreamer questions use existing pending_question + gap kind)

---

## Approach & Key Decisions

- **Watermark:** `last_processed_at` (RFC3339 timestamp) in `_system/dream_meta` — more natural than UUID for Firestore range queries.
- **Summary node_type:** New `"summary"` constant. `NodeTypeWeeklySummary` removed (unused).
- **Dreamer questions:** Use existing `kind: "gap"` on `pending_question` — no new code path needed. Dreamer answers flow through the normal refinery to extract triples.
- **LLM pattern:** Single `app.Dispatch` call (not a chat session) — dreamer needs one reflection turn only.
- **`force` parameter:** JSON body field `{"force": true}` for consistency with existing `/internal/*` handlers.
- **Cloud Scheduler:** Added to `setup-infra.sh` and idempotently re-applied in `deploy.sh`.

---

## Edge Cases & Pre-Flight Checks

1. **Concurrent dream cycles:** Two simultaneous Cloud Scheduler triggers could both read dream_meta and both commit. Mitigation: treat as idempotent (double-summary is harmless; dedup on semantic search). A Firestore transaction for watermark update would be ideal but adds complexity.
2. **Empty log set:** If `last_processed_at` is unset (first run), query all logs up to a cap (50 entries) to avoid token exhaustion.
3. **Threshold skip with force=true:** CLI/Telegram `force` flag bypasses the count/time threshold so manual triggers always run.

---

## Affected Areas

- [x] Agent / FOH loop — prompter.go adds RECENT REFLECTIONS block
- [ ] Tools — no new tools
- [x] Prompts / `app_capabilities.txt` — update after implementation
- [x] Firestore schema or queries — `_system/dream_meta` doc; existing index covers summary queries
- [x] New dependencies / infra clients — `*infra.App` passed explicitly throughout
- [x] API routes or cron jobs — `POST /internal/dream`, Cloud Scheduler
- [x] Memory / journal behavior — new `summary` node type

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

**Firestore (if applicable)**
- [ ] Composite indexes defined in `firestore.indexes.json` (already covered)
- [ ] `firebase deploy --only firestore:indexes` run (or `./scripts/deploy.sh`)

**Verification**
- [ ] `go build ./...` passes
- [ ] `go test ./...` passes
- [ ] `jot dream` triggers a summary node in Firestore
- [ ] FOH system prompt includes RECENT REFLECTIONS when summaries exist

**Wrap-up**
- [ ] `app_capabilities.txt` updated
- [ ] Brief moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260326_dreamer-system.md`
- `memory/schema.go`
- `internal/agent/dreamer.go` (new)
- `internal/agent/prompter.go`
- `internal/prompts/dreamer.txt` (new)
- `internal/prompts/prompts.go`
- `internal/prompts/system_prompt.txt`
- `pkg/system/dream_meta.go` (new)
- `internal/infra/constants.go`
- `internal/service/agent_service.go`
- `internal/api/backend.go`
- `internal/api/router.go`
- `internal/api/handler_dream.go` (new)
- `internal/api/handler_telegram_commands.go`
- `cmd/jot/main.go`
- `scripts/setup-infra.sh`
- `scripts/deploy.sh`
- `internal/prompts/app_capabilities.txt`

---

## Session Log

<!-- 20260326 -->
- Session 1: Created brief and worktree. Full codebase exploration complete. Clarifying questions resolved: summary node_type = "summary" (remove weekly_summary), watermark = timestamp, questions use kind="gap", single Dispatch LLM call, force via JSON body, Cloud Scheduler in setup-infra.sh + deploy.sh. Beginning implementation.
