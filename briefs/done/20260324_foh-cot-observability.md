# Brief: FOH Chain-of-Thought and Observability

**Date:** 20260324
**Status:** `done`
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

**Shipped:** Phases 1–3 as above. **Phase 4:** Prompt guidance for concise `<thought>` blocks; `reasoning_trace` entries capped via `truncateThoughtForTrace`; root span attributes `foh_iteration` / `foh_last_thought_len`; documented that `TrimHistory` is SDK no-op (no server-side history stripping). **Phase 5:** Removed automatic `ExpandSearchResultsToSubgraph` injection after `semantic_search`; added `graph_expand` to compact core tools; `thoughtSuggestsKnowledgeGap` merges CoT “Identified gaps:” with `knowledgeGapDetected`; `semantic_search` tool description points at `graph_expand`. Per-iteration child spans still optional (not added — use span attributes instead).

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
- [x] API routes or cron jobs — only if `QueryResult` JSON shape exposed to clients changes (`reasoning_trace` added to POST /query response).
- [ ] Memory / journal behavior (Gold vs Gravel semantics) — out unless save-query stores trace metadata.

---

## Open Questions

- [x] Are reasoning traces **debug-only** (in logs / `DebugLogs`), **returned in API JSON**, or **CLI-only**? **Resolved:** Returned as `reasoning_trace` on query result when present; also logged at Debug when `debug` is true.
- [ ] Cap or summarize thought length per iteration (max chars / rolling summary of older iterations)?
- [ ] Streaming: defer vs include in first milestone?
- [ ] Should Phase 5 (graph expansion driven by explicit reasoning) be a separate brief after Phase 1–3 ship?

---

## Checklist

**Implementation**
- [x] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Debug logs pass full strings — no truncation at Debug level
- [x] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [x] LLM output parsed as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON) where applicable for **tool** and **flat** LLM outputs; CoT blocks may use regex extraction as an exception — document in code comment
- [x] Every significant agentic step has `StartSpan` / `defer span.End()` (existing `query.run`; iteration sub-spans deferred)
- [x] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines (`foh.go` already over limit; refactor separate)

**Firestore (if applicable)**
- [x] N/A — no schema change
- [x] N/A

**Verification (Proof of Work)**

- [x] **Compilation:** `go build ./...` passes cleanly.
- [x] **Tests:** `go test ./...` passes (all packages OK).
- [x] **Lint/Format:** `gofmt` + `go vet ./...` pass.
- [ ] **Manual Smoke Test:** POST `/query` or CLI with `debug` enabled; confirm `reasoning_trace` appears when the model emits `<thought>` blocks.

**Wrap-up**
- [x] `app_capabilities.txt` updated if capabilities changed
- [x] `blueprint.md` consulted if core agentic loop was touched
- [x] Tests added / updated (`foh_thought_test.go`)
- [x] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

List the files Cursor should @mention at session start. Keep this tight — only what's directly touched by this feature.

- `briefs/done/20260324_foh-cot-observability.md` (this file)
- `internal/agent/foh.go`
- `internal/agent/foh_thought.go`
- `internal/prompts/system_prompt.txt`
- `blueprint.md`
- `internal/prompts/app_capabilities.txt`
- `internal/api/backend.go`

---

## Session Log

_The LLM appends a short bullet summary here at the end of each session. Most recent first._

Context Management: When appending to the Session Log in the active brief, you must proactively "compact" older entries. If the log exceeds 5 bullet points, summarize the older points into a single "Prior Context" bullet. Keep the brief dense and token-efficient.

<!-- 20260324 -->
- Completed Phases 4–5: concise-thought prompt + trace truncation + span iteration attrs; agent-driven graph (`graph_expand` in core tools, auto subgraph inject removed); `thoughtSuggestsKnowledgeGap`; docs (`app_capabilities`, `blueprint`, brief).
- Prior Context: Phases 1–3 (CoT protocol, `ReasoningTrace`, tests); brief/worktree creation on `feature/foh-cot-observability`.
