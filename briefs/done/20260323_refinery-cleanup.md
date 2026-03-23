# Brief: Codebase Alignment & Refinery Optimization (V2 Clean-up)

**Date:** 20260323
**Status:** `done`
**Branch:** `feature/refinery-cleanup`
**Worktree:** `../jot-refinery-cleanup`

## Goal
Resolve triple redundancy by making Refinery the single semantic extraction path and fix unlinked graph breadcrumbs.

## Scope
**In:** remove evaluator/FOH legacy extraction, centralize ontology, add entity types, update log `entity_links`, align docs.
**Out:** background SPO extraction and manual FOH fact extraction.

## Checklist
**Implementation**
- [x] Remove `fact_to_store` from evaluator prompt/logic.
- [x] Remove forced `upsert_knowledge` extraction instructions from system prompt.
- [x] Define centralized allowed predicates in `memory/schema.go`.
- [x] Inject ontology into refinery prompt data.
- [x] Respect extractor-provided entity node types when ensuring nodes.
- [x] Update refinery commit to link source log to relationship nodes.

**Verification (Proof of Work)**
- [x] `go test ./...`
- [x] `go build ./...`
- [ ] Manual smoke test path for "I moved to San Francisco."

## Session Log
<!-- 20260323 -->
- Merged `feature/refinery-cleanup` back to `main`, closed out worktree, and moved this brief to done.
- Verified `go test ./...` and `go build ./...` pass in `../jot-refinery-cleanup`; manual ingest smoke test still pending runtime environment.
- Created feature worktree and implemented V2 refinery cleanup changes across evaluator, prompts, refinery pipeline, schema, and docs.
