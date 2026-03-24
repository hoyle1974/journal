# Brief: Project Loom — Sequential Waterfall Ingestion Pipeline

**Date:** 20260323
**Status:** `in-progress`
**Branch:** `feature/project-loom`
**Worktree:** `../jot-project-loom`

---

## Goal

Refactor the AI memory graph ingestion pipeline from a loosely-ordered set of async side-effects into a "Strictly Sequential Waterfall Pipeline" (Project Loom) that guarantees complete Knowledge Graph and Task state before generating a user response node.

---

## Scope

**In:**
- `memory/schema.go` — add NodeTypeObject, NodeTypeResponse, GraphNodeMeta struct, CanonicalMapConfig struct
- `internal/agent/process_entry.go` — add ProcessLogSequential with waterfall stages
- `internal/agent/loom_workers.go` — runTaskWorker and runResponseWorker (Phase 2 stubs, Phase 4 full impl)
- `internal/agent/refinery_pipeline.go` — dynamic canonical map, NEW: predicate handling, updateHotEdges
- `internal/agent/decay_cron.go` — nightly relevance decay cron stub

**Out:**
- Existing `ProcessEntry` function — kept intact; `ProcessLogSequential` is additive
- FOH loop (`foh.go`) — not changed in this brief
- Existing tool implementations

---

## Approach & Key Decisions

- `ProcessLogSequential` is a new function alongside `ProcessEntry` (not a replacement). Callers can migrate incrementally.
- `CanonicalMapConfig` singleton stored at Firestore path `_config/canonical_map` (outside `journal` node space).
- `GraphNodeMeta` fields (`RelevanceScore`, `HotEdges`) added as top-level Firestore fields on `KnowledgeNode` so they are queryable for hot-edge eviction.
- Phase 4 Response Worker runs after Refinery+Tasks are complete, stores a `NodeTypeResponse` node — treated as async proactive insight for background entries (Telegram etc.), not a real-time synchronous reply.
- Decay cron is a stub with math stub — not wired to scheduler yet.

---

## Edge Cases & Pre-Flight Checks

1. **CanonicalMapConfig cold start:** First run before the `_config/canonical_map` document exists must fall back to `memory.AllowedPredicates` without error.
2. **Hot Edge Eviction N+1:** Fetching 20 relationship docs individually for score comparison is O(20) Firestore reads per new edge. Acceptable for now; flag for batched reads in future.
3. **ProcessLogSequential vs ProcessEntry callers:** Both must be able to coexist — existing callers of `ProcessEntry` must not break.

---

## Affected Areas

- [x] Firestore schema or queries — `GraphNodeMeta` adds `relevance_score`, `hot_edges` fields; `_config/canonical_map` doc
- [x] Memory / journal behavior — new node types, new waterfall stages
- [x] Agent / FOH loop — `ProcessLogSequential` is additive, existing callers unchanged

---

## Open Questions

- [ ] Should `ProcessLogSequential` replace `ProcessEntry` in the Cloud Task handler once stable, or be a parallel path?
- [ ] Should `_config/canonical_map` be seeded in a migration/init step?

---

## Key Files

```
briefs/active/20260323_project-loom.md
memory/schema.go
internal/agent/process_entry.go
internal/agent/refinery_pipeline.go
internal/agent/loom_workers.go
internal/agent/decay_cron.go
docs/superpowers/plans/2026-03-23-project-loom-phases-1-2.md
docs/superpowers/plans/2026-03-23-project-loom-phases-3-4.md
docs/superpowers/plans/2026-03-23-project-loom-phase-5.md
```

---

## Session Log

<!-- 20260323 -->
- Created brief and worktree for Project Loom refactor. Writing 3 plans (phases 1+2, 3+4, 5). Executing phases 1+2 inline.
