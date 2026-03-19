# Brief: Centroid-Based Context Incubation

**Date:** 20260318
**Status:** `in-progress`
**Branch:** `feature/centroid-incubation`
**Worktree:** `../jot-centroid-incubation`

---

## Goal

Surface "invisible" themes where the user (or the specialist) failed to provide an explicit tag but a semantic cluster is forming in memory. Uses math-driven cluster detection and auto-naming of synthetic clusters rather than relying on tags.

---

## Scope

**In:**
- Math-driven cluster detection via cosine similarity
- Auto-naming of synthetic clusters via LLM
- Fixed distance-radius threshold (not dynamic K)

**Out:**
- K-Means with dynamic K

---

## Approach & Key Decisions

The Proximity Check: Every night, the Dreamer queries all `knowledge_nodes` from the last 7 days with `significance_weight >= 0.7`.

The Cluster Logic: Iteratively group facts within a 0.88 Cosine Similarity radius of each other using mean centroid vectors.

Auto-Labeling: If a group of ≥5 facts is found that isn't currently linked to an active Context, send that group to an LLM to "Name this project/theme." Output format: K/V lines.

Promotion: Call `EnsureContextExists` with the generated name and link the cluster members.

Wiring: Runs inside `RunDreamer` after the Specialist extraction phase but before synthesis.

---

## Edge Cases & Pre-Flight Checks

1. Knowledge nodes may not have embedding vectors stored — need to check whether embeddings exist and skip or generate on-demand.
2. A cluster might partially overlap an existing active Context; auto-naming could create a near-duplicate. Check existing context names before calling `EnsureContextExists`.

---

## Affected Areas

- [x] Agent / FOH loop — review `blueprint.md` before changing (wired into Dreamer)
- [x] Prompts / `app_capabilities.txt` — update if Jot's capabilities change
- [ ] Firestore schema or queries — check if embedding field is queryable
- [x] New dependencies / infra clients — pass via `*infra.App`, never hidden in context
- [ ] API routes or cron jobs
- [x] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- [ ] Are embedding vectors currently stored on `knowledge_nodes` in Firestore? If not, does the Dreamer generate them?
- [ ] What is the embedding dimension used?
- [ ] Is `EnsureContextExists` an existing function in `pkg/memory`?

---

## Checklist

**Implementation**
- [ ] Math: Create `pkg/utils/centroid.go` — cosine similarity and mean vector
- [ ] Agent: Create `internal/agent/centroid_incubator.go` — batch processing logic
- [ ] Integration: Wire into `RunDreamer` after Specialist extraction, before synthesis
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
- [ ] `blueprint.md` consulted — core Dreamer loop is touched
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260318_centroid-incubation.md` (this file)
- `pkg/utils/centroid.go` (new)
- `internal/agent/centroid_incubator.go` (new)
- `internal/agent/dreamer.go` (wiring)
- `pkg/memory/incubation.go` (context promotion)
- `blueprint.md` (consult before changing Dreamer)

---

## Session Log

<!-- 20260318 -->
- Brief created; worktree and branch created; parallel agent dispatched to implement.
