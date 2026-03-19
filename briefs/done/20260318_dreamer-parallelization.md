# Brief: Dreamer Parallelization

**Date:** 20260318
**Status:** `in-progress`
**Branch:** `feature/dreamer-parallelization`
**Worktree:** `../jot-dreamer-parallelization`

---

## Goal

Dramatically reduce dream sequence runtime (currently 3–5 min for 25 entries) by parallelizing independent LLM calls across four areas: the colloquium per-pass, the extraction phase, fact writes, and context synthesis. Also moves `RunContextExtractor` earlier so context names inform the colloquium from the start.

---

## Scope

**In:**
- Move `RunContextExtractor` (2b) to before the Colloquium; inject results into the initial room transcript
- Parallelize colloquium: fan out all 5 `RunSpecialistDiscussion` calls per pass with `errgroup`
- Parallelize extraction phase: all 5 `RunSpecialist` + `RunQueryAnalyzer` in one `errgroup`
- Parallelize `dreamerWriteMergedFacts`: fan out `UpsertSemanticMemoryPreembedded` with semaphore (cap 5)
- Parallelize `dreamerSynthesizeContexts`: fan out `SynthesizeContext` with `errgroup`

**Out:**
- Changes to prompt content (beyond injecting context names into room transcript seed)
- Changes to `RunCommittee`, task phase, profile synthesis, evolution synthesis
- Any new Firestore indexes

---

## Approach & Key Decisions

- All parallelism uses `errgroup.WithContext` (already used in `RunCommittee` in specialists.go)
- Colloquium parallelization means agents see prior-pass messages but not same-pass colleagues' messages — acceptable tradeoff (less order bias, debate still happens across passes)
- Fact writes capped at 5 concurrent to avoid Firestore rate limits
- `RunContextExtractor` result injected into initial `roomTranscript` seed string (no signature changes to `RunSpecialistDiscussion`)

---

## Edge Cases & Pre-Flight Checks

1. **Colloquium `allDone` logic** — currently sequential: if any agent is not-done, `allDone = false`. With parallel fan-out, must collect `isDone` from all goroutines before evaluating `allDone`.
2. **Fact write race on same Firestore doc** — two facts from different domains could collide on the same near-neighbor. Low probability (different domain text), but each upsert is already self-contained (FindNearest + conditional write). Acceptable.
3. **Context for goroutines** — must use `gctx` (from `errgroup.WithContext`) inside goroutines, not outer `ctx`, so cancellation propagates correctly.

---

## Affected Areas

- [x] Agent / FOH loop — `dreamer.go` is the core; `blueprint.md` consulted
- [ ] Tools
- [ ] Prompts / `app_capabilities.txt`
- [ ] Firestore schema or queries
- [ ] New dependencies / infra clients
- [ ] API routes or cron jobs
- [ ] Memory / journal behavior

---

## Key Files

- `briefs/active/20260318_dreamer-parallelization.md` (this file)
- `internal/agent/dreamer.go`
- `internal/agent/specialists.go`
- `pkg/memory/knowledge.go`
- `pkg/memory/context.go`

---

## Session Log

<!-- 20260318 -->
- Session started. Analyzed dream sequence bottlenecks (32 sequential LLM calls → 3–5 min). Design approved by user. Worktree created. Writing implementation plan.
