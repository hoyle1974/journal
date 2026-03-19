# Brief: Relational SPO Triple Extraction

**Date:** 20260318
**Status:** `in-progress`
**Branch:** `feature/spo-triple-extraction`
**Worktree:** `../jot-spo-triple-extraction`

---

## Goal

Move Jot from flat text nodes to a "Semantic Web" where the agent can follow explicit edges between entities. Extracts Subject | Predicate | Object (SPO) triples and stores edges in `entity_links`, enabling multi-hop graph traversal in recall.

---

## Scope

**In:**
- Extracting SPO triples; storing edges in `entity_links`
- 1-hop traversal in `get_entity_network`

**Out:**
- Complex graph queries (Cypher/SPARQL)
- Dynamic multi-hop beyond 1 level (for now)

---

## Approach & Key Decisions

Schema Change: Update `KnowledgeNode` metadata to include a `predicate` field (e.g., `works_at`, `prefers`, `is_part_of`).

Specialist Upgrade: Re-prompt the Anthropologist and Architect specialists to extract facts in triple format: `Subject | Predicate | Object`.

Example: `[Sarah] -> [prefers] -> [Oat Milk]`.

Recursive Recall: Enhance `get_entity_network`:
1. Fetch the root entity
2. Retrieve all nodes listed in `entity_links` (1-hop traversal)
3. Synthesize the profile based on edges found

---

## Edge Cases & Pre-Flight Checks

1. Not all facts are relational — some are attributes (e.g., "Sarah's birthday is March 5"). The specialist prompt must handle both triple format and flat attribute format gracefully.
2. The `object` in a triple may or may not correspond to an existing entity UUID. The schema update must handle both UUID references and raw string values.

---

## Affected Areas

- [x] Agent / FOH loop — review `blueprint.md` before changing
- [x] Tools — `get_entity_network` refactored; register via `tools.Register()` in `init()`
- [x] Prompts / `app_capabilities.txt` — specialist prompts updated; capabilities change
- [ ] Firestore schema or queries — `entity_links` field updated
- [x] New dependencies / infra clients — pass via `*infra.App`, never hidden in context
- [ ] API routes or cron jobs
- [x] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- [ ] Do `entity_links` currently store UUIDs of related nodes, or raw strings?
- [ ] Which specialists (Anthropologist, Architect) need prompt updates?
- [ ] Is there a shared `specialist_base.txt` prompt or are they separate files?

---

## Checklist

**Implementation**
- [ ] Prompts: Update `internal/prompts/specialist_*.txt` to output triples: `Subject | Predicate | Object`
- [ ] Metadata: Update `pkg/memory/schema.go` to include `predicate` and `object_uuid` in metadata structs
- [ ] Tooling: Refactor `get_entity_network` in `memory_tools.go` to perform multi-node fetch and edge-aware synthesis
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] User-origin strings wrapped with `WrapAsUserData()` in any prompt
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

- `briefs/active/20260318_spo-triple-extraction.md` (this file)
- `internal/prompts/specialist_*.txt` (update)
- `pkg/memory/schema.go` (update)
- `internal/tools/impl/memory_tools.go` (update `get_entity_network`)
- `blueprint.md` (consult)

---

## Session Log

<!-- 20260318 -->
- Brief created; worktree and branch created; parallel agent dispatched to implement.
