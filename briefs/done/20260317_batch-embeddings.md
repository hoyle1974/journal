# Brief: Batch Embedding Support

**Date:** 20260317
**Status:** `done`
**Branch:** `feature/batch-embeddings`
**Worktree:** `../jot-batch-embeddings`

---

## Goal

Add a `GenerateEmbeddingsBatch` function to `internal/infra/gemini.go` that sends multiple texts in a single Vertex AI request (up to 250 instances). Use it in the dream sequence to eliminate the N×2 sequential embedding calls: once during `mergeDreamerFacts` (consolidation) and once per fact inside `UpsertSemanticMemory`. This should cut embedding latency in the dream run from O(N) round-trips to O(1) or a small constant.

---

## Scope

**In:**
- Add `GenerateEmbeddingsBatch(ctx, projectID, texts []string, taskType) ([][]float32, error)` to `internal/infra/gemini.go`
- Update `mergeDreamerFacts` in `internal/agent/dreamer.go` to pre-compute all fact embeddings in one batch call, pass them through to `UpsertSemanticMemory` so the second embedding is skipped
- Cache/reuse the token source rather than fetching it fresh per call (bonus fix)

**Out:**
- Parallelizing the specialist agents (separate concern)
- Changes to context synthesis LLM calls
- `FindContextByName` in-memory scan optimization

---

## Approach & Key Decisions

The Vertex AI `text-embedding-005:predict` endpoint already accepts an `instances` array with up to 250 items and returns a `predictions` array of the same length. The current `GenerateEmbedding` always sends exactly one instance. We add a batch variant that sends N instances and returns N vectors.

To avoid threading embeddings through every call site, `UpsertSemanticMemory` gains an optional `precomputedEmbedding []float32` parameter (or a separate internal variant). The dream sequence pre-computes embeddings once, then passes them in. Non-dream callers continue using the single-call path unchanged.

Token source reuse: move `google.DefaultTokenSource` construction outside the per-request path — either accept it as a parameter or cache it at the infra level.

---

## Edge Cases & Pre-Flight Checks

1. **Batch size limit:** Vertex AI allows max 250 instances per request. If the dream produces >250 facts (unlikely but possible), the batch function must chunk automatically.
2. **Partial failures:** The API may return fewer predictions than instances (or error on the whole batch). Need to validate `len(predictions) == len(texts)` and surface a clear error.
3. **Embedding reuse contract:** Passing a precomputed embedding into `UpsertSemanticMemory` skips the KNN duplicate check's embedding — must ensure the same task type (`RETRIEVAL_DOCUMENT`) is used consistently, or the cosine similarity comparisons will be against mismatched vector spaces.

---

## Affected Areas

- [x] Agent / FOH loop — `mergeDreamerFacts` in `dreamer.go` changes
- [ ] Tools — no new tools
- [ ] Prompts / `app_capabilities.txt` — no capability change
- [ ] Firestore schema or queries — no index changes
- [x] New dependencies / infra clients — token source caching in `internal/infra/gemini.go`
- [ ] API routes or cron jobs
- [ ] Memory / journal behavior — `UpsertSemanticMemory` signature change in `pkg/memory/knowledge.go`

---

## Open Questions

- [ ] Does `UpsertSemanticMemory` need a new exported variant or an internal bypass? Prefer addding an optional param to avoid breaking all callers.
- [ ] Should token source caching be scoped to the `*infra.App` struct or a package-level sync.Once?

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
- N/A

**Verification (Proof of Work)**
- [ ] **Compilation:** `go build ./...` passes cleanly.
- [ ] **Tests:** `go test ./...` passes.
- [ ] **Lint/Format:** Code is formatted and passes `go vet`.
- [ ] **Manual Smoke Test:** Trigger a dream run and confirm embedding call count drops (check logs/traces).

**Wrap-up**
- [ ] `app_capabilities.txt` updated if capabilities changed
- [ ] `blueprint.md` consulted if core agentic loop was touched
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

briefs/active/20260317_batch-embeddings.md (this file)
internal/infra/gemini.go
internal/agent/dreamer.go
pkg/memory/knowledge.go

---

## Session Log

_The LLM appends a short bullet summary here at the end of each session. Most recent first._

<!-- 20260317 -->
- Implementation complete. `go build ./...` and `go test ./...` both pass (all green).
- Added `GenerateEmbeddingsBatch` to `internal/infra/gemini.go`: sends up to 250 texts per HTTP request, auto-chunks, reuses token fetch across chunks.
- Rewrote `mergeDreamerFacts` in `internal/agent/dreamer.go`: collects all facts first, batch-embeds in one call, attaches vectors to `mergedFact.Vector`.
- Added `UpsertSemanticMemoryPreembedded` + `upsertSemanticMemoryWithVector` to `pkg/memory/knowledge.go`: dream write path skips second embedding entirely; non-dream callers unchanged.
- Created brief; identified two-phase embedding bottleneck (merge + upsert = 2× N calls).
