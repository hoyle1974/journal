# Brief: FOH Chain-of-Thought and Observability

**Date:** 20260324
**Status:** `in-progress`
**Branch:** `feature/foh-cot-observability`
**Worktree:** `../jot-foh-cot-observability`

---

## Goal

Make the Front-of-House (FOH) agent’s reasoning **explicit and inspectable**: a structured per-iteration rationale (plan, identified gaps, next step, refinements), optional surfacing of raw vs extracted thought in debug mode, and tracing hooks (`StartSpan` child spans per iteration) so developers can verify *why* tools ran and how synthesis relates to retrieval. This addresses “logical drift” and black-box behavior in the existing iterative Reason–Act loop without abandoning Jot’s established tool protocol (K/V structured tool calls).

---

## Scope

**In:**
- Prompt-level **Reasoning Protocol** (mandatory blocks before tool use / final answer), implemented via existing `text/template` + typed prompt data (no stringly `fmt.Sprintf` assembly).
- **Parsing** of optional delimited reasoning (e.g. `<thought>...</thought>`) in `RunQueryWithDebug` after each model response; accumulation into a **reasoning trace** (new field on `QueryResult` or parallel to `DebugLogs`).
- **Observability:** debug path logs full reasoning text; optional span attributes / child spans per iteration; alignment with existing `QueryResult.DebugLogs` behavior.
- **History / tokens:** strategy for trimming or compacting older thought blocks while keeping recent reasoning + tool/observation history (see `MaxMessagePairs`, `TrimHistory`).
- **Documentation tradeoffs:** token/latency cost of CoT (roughly 50–300 tokens per iteration × up to `MaxIterations`).
- Later-phase alignment (design only in this brief unless scoped): Graph RAG expansion and `knowledgeGapDetected` informed by reasoned gaps vs string heuristics alone.

**Out:**
- Replacing K/V tool invocation with JSON or unstructured tool JSON from the model.
- Full streaming parser / early-termination streaming (unless explicitly added in a follow-up scope).
- Firestore schema or composite index changes unless `/query` or save-query persistence needs new fields (then scoped explicitly).

---

## Approach & Key Decisions

**Phased milestones (source: architectural evolution doc; anchor to codebase):**

| Phase | Milestone | Implementation anchor |
| ----- | --------- | --------------------- |
| 1 | Reasoning protocol in system prompt | `internal/prompts/system_prompt.txt`, `internal/prompts/prompts.go` — extend typed `...Data` for CoT instructions (first-turn plan, per-iteration thought schema). |
| 2 | Parse `<thought>` (or agreed delimiter) in the loop | `internal/agent/foh.go` — after `SendMessage` / response, extract thought before `ParseStructuredToolCall`; append to trace; warn if missing, continue for backward compatibility. |
| 3 | `QueryResult` + debug | Extend `QueryResult` with e.g. `ReasoningTrace []string` or structured steps; mirror into `LoggerFrom(ctx).Debug` when `debug` is true; API/CLI only if we expose trace beyond debug. |
| 4 | History / trim | Revisit `TrimHistory` / `MaxMessagePairs`: optionally keep full tool+result history while summarizing or dropping oldest thought blocks. |
| 5 | Graph RAG + gaps | `ExpandSearchResultsToSubgraph`, `knowledgeGapDetected`, `extractMissingInfoAndAnswer` — optional agent-stated justification for expansion; qualitative gap lines vs binary string match (incremental). |

**K/V vs XML coexistence (critical):** Jot requires tool calls as **key/value structured output** (`ParseStructuredToolCall`), not JSON from the LLM. CoT tags are **orthogonal**: the model may emit `<thought>...</thought>` (and plain-text final answer) while tool calls remain the existing K/V protocol. If the model omits tags, behavior degrades gracefully to today’s flow.

**Prompt engineering:** Use `text/template` and typed structs per `.cursorrules`; wrap user-origin strings with `WrapAsUserData()` where applicable.

---

## Edge Cases & Pre-Flight Checks

1. **Missing or malformed `<thought>` blocks:** Log a warning, proceed with tool parsing and final answer extraction so existing flows do not break.
2. **Token pressure:** Verbose CoT across 10 iterations can add thousands of tokens; trimming/compaction strategy must not drop tool results needed for correctness.
3. **Tag leakage:** Model might emit `<thought>` inside tool K/V or user-visible answer; parsing must strip or segment so `extractMissingInfoAndAnswer` and user-facing text stay correct.
4. **Tool repeat backoff:** CoT should reduce loops, but existing `ToolRepeatBackOffAt` remains; ensure backoff messages do not duplicate or fight the reasoning protocol.
5. **Multi-model Gemini:** Reasoning schema must be strict enough that flash vs pro behave consistently; add tests or golden samples if needed.

---

## Affected Areas

_Check all that apply and note specifics:_

- [x] Agent / FOH loop — review `blueprint.md` before changing (`RunQueryWithDebug`, iteration limits, `retrievedContent`, subgraph expansion).
- [ ] Tools — register via `tools.Register()` in `init()`, co-locate by domain (only if tool descriptions must reference CoT protocol).
- [x] Prompts / `app_capabilities.txt` — update if Jot’s observable behavior or user-facing capabilities change.
- [ ] Firestore schema or queries — update `firestore.indexes.json` if new composite indexes needed.
- [x] New dependencies / infra clients — pass via `*infra.App`, never hidden in context.
- [ ] API routes or cron jobs — only if `QueryResult` JSON shape exposed to clients changes.
- [ ] Memory / journal behavior (Gold vs Gravel semantics) — out unless save-query stores trace metadata.

---

## Open Questions

- [ ] Are reasoning traces **debug-only** (in logs / `DebugLogs`), **returned in API JSON**, or **CLI-only**?
- [ ] Cap or summarize thought length per iteration (max chars / rolling summary of older iterations)?
- [ ] Streaming: defer vs include in first milestone?
- [ ] Should Phase 5 (graph expansion driven by explicit reasoning) be a separate brief after Phase 1–3 ship?

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [ ] LLM output parsed as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON) where applicable for **tool** and **flat** LLM outputs; CoT blocks may use regex extraction as an exception — document in code comment
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Firestore (if applicable)**
- [ ] Composite indexes defined in `firestore.indexes.json`
- [ ] `firebase deploy --only firestore:indexes` run (or `./scripts/deploy.sh`)

**Verification (Proof of Work)**
_The AI must complete these steps and paste the final successful output or command used before marking this brief as done._

- [ ] **Compilation:** `go build ./...` passes cleanly.
- [ ] **Tests:** `go test ./...` passes. (Paste relevant test output below).
- [ ] **Lint/Format:** Code is formatted and passes `go vet`.
- [ ] **Manual Smoke Test:** POST `/query` or CLI with `debug` enabled; confirm reasoning trace appears in logs and behavior matches acceptance criteria.

**Wrap-up**
- [ ] `app_capabilities.txt` updated if capabilities changed
- [ ] `blueprint.md` consulted if core agentic loop was touched
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

List the files Cursor should @mention at session start. Keep this tight — only what's directly touched by this feature.

- `briefs/active/20260324_foh-cot-observability.md` (this file)
- `internal/agent/foh.go`
- `internal/prompts/system_prompt.txt`
- `internal/prompts/prompts.go`
- `blueprint.md`
- `internal/prompts/app_capabilities.txt`
- `internal/infra/` (spans, chat session)

---

## Session Log

_The LLM appends a short bullet summary here at the end of each session. Most recent first._

Context Management: When appending to the Session Log in the active brief, you must proactively "compact" older entries. If the log exceeds 5 bullet points, summarize the older points into a single "Prior Context" bullet. Keep the brief dense and token-efficient.

<!-- 20260324 -->
- Created brief; added worktree `../jot-foh-cot-observability` on branch `feature/foh-cot-observability` (from main `fecc61a`). No implementation yet.
