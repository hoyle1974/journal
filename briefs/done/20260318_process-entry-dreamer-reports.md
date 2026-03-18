# Brief: Process-Entry and Dreamer Debug Reports

**Date:** 20260318
**Status:** `done`
**Branch:** `feature/process-entry-reports`
**Worktree:** `../jot-process-entry-reports`

---

## Goal

Generate a first-person LLM narrative report to Google Doc for both the `process-entry` Cloud Task and the Dreamer, mirroring the existing query debug report. The report is gated on the `DebugReportEnabled` config flag and gives operators visibility into what the agent reasoned during entry processing and dream cycles — the same observability already available for the FOH query path.

---

## Scope

**In:**
- `ProcessEntryReport` struct in `internal/agent/process_entry.go`
- `GenerateProcessEntryReport` in `internal/agent/report.go`
- Prompt template `internal/prompts/process_entry_report_prompt.txt` + registration in `prompts.go`
- Async report wiring in `internal/api/handler_tasks.go` (`handleProcessEntry`)
- `AgentService.ProcessEntry` and `AgentHandler` interface updated in `agent_service.go` / `backend.go`
- Dreamer: `writeDreamNarrative` returns `(string, error)`; `RunDreamer` fires `gdoc.WriteReport` via `SubmitAsync`

**Out:**
- New config flags, changes to FOH/query path, changes to Dreamer's LLM calls

---

## Approach & Key Decisions

Mirrored the existing `GenerateQueryReport` pattern: collect a `ProcessEntryReport` struct during `ProcessEntry`, pass it back to the handler, then fire `gdoc.WriteReport` asynchronously via `SubmitAsync` when `DebugReportEnabled` is true. For the Dreamer, `writeDreamNarrative` was changed to return its narrative string so `RunDreamer` can hand it to `gdoc.WriteReport` without a separate LLM call.

---

## Edge Cases & Pre-Flight Checks

1. Async goroutine for report writing must not block the main request path — handled via `SubmitAsync`.
2. `DebugReportEnabled` guard must be checked before spawning the goroutine to avoid unnecessary LLM calls in production.

---

## Affected Areas

- [x] Agent / FOH loop — `dreamer.go` touched; `blueprint.md` consulted
- [ ] Tools
- [ ] Prompts / `app_capabilities.txt`
- [ ] Firestore schema or queries
- [ ] New dependencies / infra clients
- [x] API routes or cron jobs — `handler_tasks.go` wiring
- [ ] Memory / journal behavior

---

## Open Questions

- None remaining.

---

## Checklist

**Implementation**
- [x] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Debug logs pass full strings — no truncation at Debug level
- [x] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [x] LLM output parsed as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON)
- [x] Every significant agentic step has `StartSpan` / `defer span.End()`
- [x] Errors wrapped with `%w`, not `%v`
- [x] No file exceeds 400 lines

**Firestore (if applicable)**
- N/A

**Verification (Proof of Work)**
- [x] **Compilation:** `go build ./...` passes cleanly.
- [x] **Tests:** `go test ./...` passes.
- [x] **Lint/Format:** Code is formatted and passes `go vet`.

**Wrap-up**
- [x] `app_capabilities.txt` updated if capabilities changed
- [x] `blueprint.md` consulted if core agentic loop was touched
- [x] Tests added / updated
- [x] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/done/20260318_process-entry-dreamer-reports.md` (this file)
- `internal/agent/process_entry.go`
- `internal/agent/report.go`
- `internal/prompts/prompts.go`
- `internal/prompts/process_entry_report_prompt.txt`
- `internal/service/agent_service.go`
- `internal/api/backend.go`
- `internal/api/handler_tasks.go`
- `internal/agent/dreamer.go`

---

## Session Log

<!-- 20260318 -->
- Single session. Implemented process-entry report (Tasks 1–4: `ProcessEntryReport` struct, service/interface update, prompt + `GenerateProcessEntryReport`, handler wiring in `handleProcessEntry`) and dreamer report (Task 5: `writeDreamNarrative` returns narrative string, `RunDreamer` fires async `gdoc.WriteReport`). All builds clean, all tests pass.
