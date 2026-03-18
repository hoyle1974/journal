# Brief: Debug Report Narrative

**Date:** 20260318
**Status:** `done`
**Branch:** `feature/debug-report`
**Worktree:** `../jot-debug-report`

---

## Goal

When `jot "some text"` runs the FOH query loop, generate a first-person narrative report (written by an LLM) that explains what the agent did, what tools it called and why, and how it arrived at its answer. Write this report as bold text to the Google Doc in place of the normal log entry. Enabled by default; disabled via config flag.

---

## Scope

**In:**
- `GenerateDebugReport` function in `internal/agent/report.go`
- Prompt template in `internal/prompts/debug_report_prompt.txt`
- `WriteReport` function in `internal/gdoc/report.go` (writes bold narrative directly to gdoc, no timestamp prefix)
- Config flag `DebugReportEnabled bool` (default true, disabled via `JOT_DEBUG_REPORT_DISABLED=true`)
- Handler change: call `RunQueryWithDebug`, then generate+write report, suppress normal gdoc log entry for that request
- Context mechanism to suppress gdoc log forwarding for a single request

**Out:**
- CLI flag changes (server-side config flag only)
- Changes to the Dreamer, sync, or any non-query path
- Changes to the log command

---

## Approach & Key Decisions

**Option C (Hybrid)**: Use the structured `QueryResult.ToolCalls` as the skeleton, augment with decision-point lines filtered from `QueryResult.DebugLogs`, feed to a single LLM call via `app.Dispatch()` to produce a narrative.

**Narrative style**: First-person from the agent's perspective. Ends with "I responded with...". Summaries of LLM internal responses include a log timestamp/context string so the user can grep raw logs for full detail.

**Gdoc writing**: Bold text written directly via a new `gdoc.WriteReport()` function (same machinery as `logToGDocSync` but without the `timestamp: message` prefix format — the narrative is self-contained). The gdoc logger already applies bold to all writes, so this is consistent.

**Normal log entry suppression**: Add `WithSuppressGDocLog(ctx)` / `IsGDocLogSuppressed(ctx)` context helpers in `internal/infra/obs.go`. In `handleQuery`, when report mode is on, set this flag before calling `LoggerFrom(ctx).Info("query completed", ...)` so the forwarding handler skips that line.

**Config**: `config.Config.DebugReportEnabled bool` defaults to `true`. Set `JOT_DEBUG_REPORT_DISABLED=true` in env to turn off.

**Prompt**: Written as `text/template`, parsed at `init()` with `template.Must`. Input struct: `DebugReportData{Question, ToolCalls, FilteredLogs, Answer string}`.

**Filter logic for debug logs**: Keep only lines containing any of: `"FOH: iteration decision"`, `"FOH: tool call"`, `"FOH: query completed"`, `"knowledge gap"`, `"loop detected"`, `"backoff"`. These are the decision-point markers.

---

## Edge Cases & Pre-Flight Checks

1. **Report generation failure**: If the LLM call to generate the report fails, log the error but still return the query result normally. Don't fail the query because the report failed.
2. **Token limits**: If `FilteredLogs` is very large (many iterations), truncate to last 50 decision-point lines before feeding to the LLM.
3. **GDoc unavailable**: `WriteReport` can fail silently (same pattern as `logToGDocSync`) — log the error, don't bubble it up.

---

## Affected Areas

- [x] Agent / FOH loop — using `RunQueryWithDebug` instead of `RunQuery` in handler; new `report.go` in agent package
- [ ] Tools — no changes
- [ ] Prompts / `app_capabilities.txt` — no capability change, no update needed
- [ ] Firestore schema or queries — no changes
- [x] New dependencies / infra clients — none new; `app.Dispatch()` used for report LLM call
- [x] API routes or cron jobs — handler_interact.go change
- [ ] Memory / journal behavior — no changes

---

## Open Questions

- [x] Where does report appear? → Google Doc
- [x] Always or on demand? → Enabled by default, config flag to disable
- [x] Replace or alongside normal entry? → Replaces it
- [x] Level of detail? → LLM narrative, first-person, tool summaries + log timestamps for grepping
- [x] LLM internal responses? → Summarized, with timestamp marker for grep

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [ ] LLM output used as prose (not K/V parsed) — narrative is free text
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Firestore (if applicable)**
- N/A

**Verification (Proof of Work)**
- [ ] **Compilation:** `go build ./...` passes cleanly.
- [ ] **Tests:** `go test ./...` passes.
- [ ] **Lint/Format:** `go vet ./...` passes.
- [ ] **Manual Smoke Test:** `jot "what did I do yesterday?"` appends a bold narrative report to the Google Doc in place of the normal log entry.

**Wrap-up**
- [ ] `app_capabilities.txt` updated if capabilities changed
- [ ] `blueprint.md` consulted if core agentic loop was touched
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

briefs/active/20260318_debug-report.md (this file)
internal/api/handler_interact.go
internal/agent/foh.go
internal/agent/report.go (NEW)
internal/gdoc/logger.go
internal/gdoc/report.go (NEW)
internal/infra/obs.go
internal/prompts/debug_report_prompt.txt (NEW)
internal/config/config.go

---

## Session Log

<!-- 20260318 -->
- Session 2: Implemented and merged. `go build ./...` and `go test ./...` pass clean. Simplify pass fixes: manual truncation → `utils.TruncateString`, removed pre-allocation, fixed misleading comment, unexported `isGDocLogSuppressed`, moved report generation to `app.SubmitAsync` with `context.WithoutCancel`. Merged to main, worktree removed.
- Session 1: Brainstormed feature. Decided on hybrid approach (ToolCalls + filtered debug logs → LLM narrative). Report replaces normal gdoc log entry (bold). Enabled by default, config flag to disable. Created worktree `../jot-debug-report`.
