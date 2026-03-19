# Brief: Semantic Tag Consolidation (The Thesaurus)

**Date:** 20260318
**Status:** `in-progress`
**Branch:** `feature/semantic-tag-consolidation`
**Worktree:** `../jot-semantic-tag-consolidation`

---

## Goal

Fix "taxonomy drift" where synonymous tags (e.g., `jot_dev` vs `coding_jot`) prevent proper context incubation. An LLM-based tag normalization pass during the Dreamer run will canonicalize raw tags to slugs before the incubation threshold is evaluated.

---

## Scope

**In:**
- LLM-based tag normalization pass during the Dreamer run
- Canonical mapping of raw tags to "slugs"

**Out:**
- Manual tag editing UI
- Retroactive tag renaming

---

## Approach & Key Decisions

The Pivot Table: Before calculating the 2-day threshold in `PromoteIncubatingClusters`, gather all unique tags from the 7-day window.

The Normalizer: An LLM pass takes the unique list and returns a mapping: `{"coding_jot": "jot_development", "jot_dev": "jot_development"}`.

Logic Integration: Update `incubation.go` to apply this mapping when counting "distinct days," ensuring aliases aggregate toward the same context promotion goal.

Output format: K/V lines via `ParseKeyValueMap` (no JSON), one mapping per line: `raw_tag=canonical_tag`.

---

## Edge Cases & Pre-Flight Checks

1. LLM returns tag mappings where a canonical slug itself maps to another slug (chaining). Apply the mapping iteratively (or flatten transitively) to avoid incomplete normalization.
2. All tags in the window are already unique/distinct — the LLM pass adds latency with no benefit. Consider skipping if the tag set size is small (e.g., < 10 unique tags).

---

## Affected Areas

- [ ] Agent / FOH loop — review `blueprint.md` before changing
- [x] Prompts / `app_capabilities.txt` — update if Jot's capabilities change
- [ ] Firestore schema or queries — update `firestore.indexes.json` if new composite indexes needed
- [ ] New dependencies / infra clients — pass via `*infra.App`, never hidden in context
- [ ] API routes or cron jobs
- [x] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- [ ] Should the canonical slug mapping be persisted (cached in Firestore) or recomputed each Dreamer run?
- [ ] What's the right LLM temperature for deterministic normalization?

---

## Checklist

**Implementation**
- [ ] Prompt: Create `internal/prompts/tag_consolidator.txt`
- [ ] Service: Implement `ConsolidateTags(ctx, app, tags []string) map[string]string` in `internal/agent/tag_consolidator.go`
- [ ] Pkg: Modify `pkg/memory/incubation.go` to apply normalization before `themeDays` map is populated
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] LLM output parsed as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON)
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Verification (Proof of Work)**
- [ ] `go build ./...` passes cleanly
- [ ] `go test ./...` passes
- [ ] Code is formatted and passes `go vet`

**Wrap-up**
- [ ] `app_capabilities.txt` updated if capabilities changed
- [ ] `blueprint.md` consulted if core agentic loop was touched
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260318_semantic-tag-consolidation.md` (this file)
- `internal/prompts/tag_consolidator.txt` (new)
- `internal/agent/tag_consolidator.go` (new)
- `pkg/memory/incubation.go`
- `internal/agent/dreamer.go` (wiring)

---

## Session Log

<!-- 20260318 -->
- Brief created; worktree and branch created; parallel agent dispatched to implement.
