# Brief: Refactoring Agent Orchestration to Unified CoT

**Date:** 20250316
**Status:** done
**Branch:** `feature/unified-cot-agent` (merged to main)
**Worktree:** removed

---

## Goal

Reduce codebase complexity by eliminating the multi-stage Go orchestration (Reflection, Synthesis, and Knowledge Gap passes) in the FOH query loop. Instead, leverage the LLM's innate reasoning to perform these audits within a single, high-context generation window. The loop becomes a single ReAct cycle where "Audit" (reflection + gap detection + synthesis) is a required part of the model's scratchpad, reducing API round-trips and moving behavior into prompts rather than compiled state-machine logic.

---

## Scope

**In:**
- Updating system prompt / `internal/prompts` so Reflection, Gap Detection, and Synthesis are required in the model's thought block before final answer.
- Replacing multi-step RunQuery logic in `internal/agent/foh.go` with a simplified loop that expects a structured "Thought/Audit/Action/Answer" (or equivalent) format.
- Extracting Audit and Missing fields from the LLM's structured output and logging knowledge gaps when present.
- Removing the existing reflection check, reflection revision, and synthesis pass code from `foh.go` (~150–200 lines of state-machine handling).
- Updating `app_capabilities.txt` and `blueprint.md` to reflect the new FOH behavior.

**Out:**
- Changing Dreamer's reflection/synthesis (e.g. `dreamer_synthesis.go`, Dreamer gap detection) — those stay as-is.
- Changing persona layer or tool registration.

---

## Approach & Key Decisions

**Current state (verbose Go logic):**
- Initial thought → tool execution → **Reflection pass** (separate API call to verify search results vs query) → **Synthesis pass** (separate API call to combine data) → answer. Knowledge gap is tracked in Go and saved after the fact.

**Future state (prompt engineering):**
- Single ReAct cycle. The model is instructed to perform an internal audit in its thought block before providing the final answer: Reflection (does tool output answer intent? contradictions?), Gap Detection (explicitly state what is missing if not 100% sure), Synthesis (combine episodic and semantic into a cohesive narrative). Go parses structured output for `audit` and `missing_info` and logs gaps; no separate `runReflectionCheck` or `runSynthesisPass`.

**Implementation plan:**

1. **Update the system prompt** (in `internal/prompts/`, and document in `app_capabilities.txt`):
   - Add instruction snippet: "Before providing your final answer, you MUST perform an internal audit in your thought block: Reflection: Check if the tool output actually answers the user's intent. Identify contradictions. Gap Detection: If you lack specific data to be 100% sure, explicitly state what is missing. Synthesis: Combine episodic and semantic memories into a cohesive narrative."

2. **Simplified Go logic ("lean" loop):**
   - Introduce a structured response type (e.g. `UnifiedResponse` with `Thought`, `Audit`, `Missing`, `FinalReply`) and a single generation path that builds context once and runs one ReAct loop.
   - Parse the model output for Audit and Missing; call existing gap-logging when `Missing` is non-empty.
   - Remove `runReflectionCheck`, `runReflectionRevision`, and `runSynthesisPass` from `foh.go`, and remove or repurpose related prompts (e.g. `BuildReflectionCheck`, `SynthesisPass()` usage in FOH).

**Note:** There are no separate `reflection.go` or `synthesis.go` files; the code to delete lives in `foh.go` (and related prompt helpers in `internal/prompts/prompts.go` for FOH-only reflection/synthesis).

---

## Edge Cases & Pre-Flight Checks

1. **Structured output format:** The project rules say "Parse LLM output as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON)". The brief suggests JSON-like `UnifiedResponse`; we must either use a key/value or pipe-separated format and parse in Go, or get an exception for this single structured FOH response. Decide and document.
2. **Backward compatibility:** Existing callers of `RunQuery` / `RunQueryWithDebug` expect `QueryResult` with `Answer`, `Iterations`, `ToolCalls`, etc. The lean loop must preserve that contract (e.g. still return `QueryResult`, with answer = final reply).
3. **Token/context limits:** Pushing reflection + synthesis into one window increases prompt size. Confirm the system prompt plus audit instructions fit within model limits and don't crowd out tool results.

---

## Affected Areas

- [x] Agent / FOH loop — review `blueprint.md` before changing
- [ ] Tools — register via `tools.Register()` in `init()`, co-locate by domain
- [x] Prompts / `app_capabilities.txt` — update if Jot's capabilities change
- [ ] Firestore schema or queries — update `firestore.indexes.json` if new composite indexes needed
- [ ] New dependencies / infra clients — pass via `*infra.App`, never hidden in context
- [ ] API routes or cron jobs
- [ ] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- [x] Confirm output format for FOH (key/value vs structured JSON) and parsing strategy. **Decided:** Key/value; model may add one line `MISSING_INFO: <semicolon-separated list>`; parsed via `ParseKeyValueMap`, line stripped from user-facing answer.
- [x] Whether to keep a minimal "reflection fail" path in Go. **Decided:** Rely entirely on model self-audit; no separate reflection/synthesis API passes.

---

## Checklist

**Implementation**
- [x] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Debug logs pass full strings — no truncation at Debug level
- [x] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [x] LLM output parsed per project rules (key/value or agreed structured format)
- [x] Every significant agentic step has `StartSpan` / `defer span.End()`
- [x] Errors wrapped with `%w`, not `%v`
- [x] No file exceeds 400 lines

**Firestore (if applicable)**
- [x] N/A for this brief

**Verification (Proof of Work)**
- [x] **Compilation:** `go build ./...` passes cleanly.
- [x] **Tests:** `go test ./...` passes. (Paste relevant test output below).
- [x] **Lint/Format:** Code is formatted and passes `go vet`.
- [x] **Manual Smoke Test:** Run a query via CLI or POST /query and confirm answer quality and that knowledge gaps are still recorded when the model reports missing info.

**Wrap-up**
- [x] `app_capabilities.txt` updated to describe unified FOH audit behavior
- [x] `blueprint.md` updated (FOH section: single ReAct + in-prompt audit, no separate reflection/synthesis passes)
- [x] Tests added / updated
- [x] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

briefs/done/20250316_unified-cot-agent.md (this file)
internal/agent/foh.go
internal/agent/foh_test.go
internal/prompts/prompts.go
internal/prompts/app_capabilities.txt
internal/prompts/system_prompt.txt
blueprint.md

---

## Session Log

<!-- YYYYMMDD -->
- Closeout: Committed all changes in worktree; merged feature/unified-cot-agent into main; removed worktree ../jot-unified-cot-agent; moved brief to briefs/done/.
- Implemented unified CoT: added FOH audit instructions to system_prompt.txt (reflection, gap detection, synthesis in reasoning; MISSING_INFO line for gap logging). Removed runReflectionCheck, runReflectionRevision, runSynthesisPass from foh.go; added extractMissingInfoAndAnswer (key/value parse, strip MISSING_INFO from answer). Removed reflection_check/synthesis_pass from prompts.go and deleted .txt files. Updated app_capabilities.txt and blueprint.md. Added TestExtractMissingInfoAndAnswer. go build ./... and go test ./... pass in worktree.
- Brief created. Worktree `../jot-unified-cot-agent` created on branch `feature/unified-cot-agent`. All implementation must be done in the worktree directory.
