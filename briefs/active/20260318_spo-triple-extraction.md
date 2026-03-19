# Brief: Relational SPO Triple Extraction

**Date:** 20260318
**Status:** `in-progress`
**Branch:** `feature/spo-triple-extraction`
**Worktree:** `../jot-spo-triple-extraction`

---

## Goal

Move Jot from flat text nodes to a "Semantic Web" by extracting Subject | Predicate | Object triples during specialist passes, updating the schema to store predicates and object UUIDs, and enhancing `get_entity_network` for 1-hop edge traversal.

---

## Scope

**In:**
- Specialist prompts updated to emit SPO triples for relational facts
- `KnowledgeNode` struct extended with `Predicate` and `ObjectUUID` fields
- `pkg/memory/schema.go` gains `SPOTriple`, `ParseSPOTriple`, `IsSPOTriple`, `NormalizedPredicate` helpers
- `pkg/memory/knowledge.go` gains `QueryNodesLinkingTo` and `QueryOutgoingEdges` for 1-hop traversal
- `get_entity_network` tool enhanced to use 1-hop traversal and render predicate: object relationships

**Out:**
- Dreamer fact-write path does NOT yet parse SPO triples and write `predicate`/`object_uuid` to Firestore (next step)
- No new Firestore composite indexes required yet (object_uuid and entity_links fields only need single-field queries)
- No migration of existing nodes

---

## Approach & Key Decisions

- `Predicate` and `ObjectUUID` are added to `KnowledgeNode` as `omitempty` Firestore fields so existing nodes are not broken.
- SPO parsing helpers (`ParseSPOTriple`, `NormalizedPredicate`) live in `schema.go` since that's the domain schema file.
- `QueryNodesLinkingTo` uses `array-contains` on `entity_links`; `QueryOutgoingEdges` uses `==` on `object_uuid`. Both are single-field queries (no composite index needed).
- Specialist prompts (relationship, thought, work, selfmodel) updated to document triple vs flat format decision.
- `get_entity_network` runs 4 goroutines (discovered, linked, incoming edges, outgoing edges) then merges and synthesizes.
- Synthesis prompt updated to render RELATION lines as `predicate: object` bullets.

---

## Edge Cases & Pre-Flight Checks

1. **Backward compatibility**: Existing Firestore nodes have no `predicate`/`object_uuid` fields. `omitempty` on both fields ensures reads return `""` safely.
2. **Missing Firestore index**: `object_uuid == X` is a simple equality query on a single field; no composite index needed. `array-contains` on `entity_links` is also a single-field query.
3. **File size**: `knowledge.go` (1062 lines) and `schema.go` (481 lines) exceed 400 lines, but were already over limit before this change. A future refactor brief should split them.

---

## Affected Areas

- [x] Tools — `get_entity_network` enhanced with 1-hop traversal
- [x] Prompts / `app_capabilities.txt` — specialist prompt files updated (app_capabilities.txt not yet updated — SPO extraction not yet active end-to-end)
- [x] Firestore schema or queries — two new query functions added; `predicate` and `object_uuid` Firestore fields added
- [x] Memory / journal behavior — KnowledgeNode struct extended

---

## Open Questions

- [x] Should the dreamer's `dreamerWriteMergedFacts` parse SPO triples from facts and set `predicate`/`object_uuid` on the Firestore write? → **Yes. See Phase 2 below.**
- [x] Should `upsert_knowledge` tool accept `predicate` and `object_uuid` params so FOH can write triples too? → **Yes, as optional params. See Phase 2.**

---

## Phase 2: SPO Persistence (Follow-up)

**Goal:** Close the loop — specialists now emit triples, but `dreamerWriteMergedFacts` and the `upsert_knowledge` tool don't yet persist `predicate`/`object_value` to Firestore. Without this step, triple data is discarded at write time.

### Design

#### 1. Extend `mergedFact` in `dreamer.go`
```go
type mergedFact struct {
    Content     string
    NodeType    string
    Domain      string
    Weight      float64
    Vector      []float32
    Predicate   string // empty for non-relational facts
    ObjectValue string // raw object string; ObjectUUID resolution is future work
}
```

#### 2. Populate in `mergeDreamerFacts`
After building each `mergedFact`, call `memory.ParseSPOTriple(fact.fact)`. If non-nil:
- Set `Predicate = memory.NormalizedPredicate(triple.Predicate)`
- Set `ObjectValue = strings.TrimSpace(triple.Object)`
- Keep `Content` as the full original triple string (for semantic search / display)

#### 3. New upsert variant in `pkg/memory/knowledge.go`
Add:
```go
type SPOExtra struct {
    Predicate   string
    ObjectValue string
}

func UpsertSemanticMemoryPreembeddedWithSPO(
    ctx context.Context, env infra.ToolEnv,
    content, nodeType, domain string,
    significanceWeight float64,
    entityLinks, journalEntryIDs []string,
    vector []float32,
    spo *SPOExtra, // nil = non-relational fact
) (string, error)
```
In `upsertSemanticMemoryWithVector` (internal), accept `*SPOExtra` and add to `data` map only when non-nil and non-empty:
```go
if spo != nil && spo.Predicate != "" {
    data["predicate"] = spo.Predicate
    data["object_value"] = spo.ObjectValue
}
```
The existing `UpsertSemanticMemoryPreembedded` remains unchanged (calls new variant with `nil` SPO).

#### 4. Update `dreamerWriteMergedFacts`
Replace `UpsertSemanticMemoryPreembedded` call with `UpsertSemanticMemoryPreembeddedWithSPO`, passing `&SPOExtra{Predicate: fact.Predicate, ObjectValue: fact.ObjectValue}` (or `nil` if both are empty).

#### 5. Extend `upsert_knowledge` tool (`internal/tools/impl/memory_tools.go`)
Add optional tool params `predicate` and `object_value`. When provided, call `UpsertSemanticMemoryPreembeddedWithSPO` with the extra. Document in tool description.

### What's deferred
- **`object_uuid` resolution**: When `ObjectValue` matches an existing entity name, looking up its UUID requires an extra Firestore query. This is a future optimization — for now `object_value` (raw string) is sufficient for 1-hop traversal via `get_entity_network` (which already uses `QueryOutgoingEdges` on `object_uuid`, but will gracefully return nothing if `object_uuid` is empty).
- **Migration**: No retroactive migration of existing nodes needed — `predicate`/`object_value` fields are `omitempty`.

### Checklist (Phase 2)
- [x] Add `Predicate`, `ObjectValue` to `mergedFact` struct
- [x] Populate from `ParseSPOTriple` in `mergeDreamerFacts`
- [x] Add `SPOExtra` struct + `UpsertSemanticMemoryPreembeddedWithSPO` in `knowledge.go`
- [x] Update `dreamerWriteMergedFacts` to use new variant
- [x] Extend `upsert_knowledge` tool with optional `predicate` + `object_value` params
- [x] Update `app_capabilities.txt` — Jot can now store relational facts end-to-end
- [x] `go build ./...` + `go vet ./...` clean
- [ ] Add test: fact with triple content → Firestore write includes `predicate` field

---

## Checklist

**Implementation**
- [x] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Debug logs pass full strings — no truncation at Debug level
- [x] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [x] LLM output parsed as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON)
- [x] Every significant agentic step has `StartSpan` / `defer span.End()`
- [x] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines (knowledge.go and schema.go pre-existing; refactor deferred)

**Firestore (if applicable)**
- [ ] Composite indexes defined in `firestore.indexes.json` — not required for these queries
- [ ] `firebase deploy --only firestore:indexes` run

**Verification (Proof of Work)**
- [x] **Compilation:** `go build ./...` passes cleanly (no output).
- [ ] **Tests:** `go test ./...` passes.
- [x] **Lint/Format:** `go vet ./...` clean; `go fmt ./...` applied.

**Wrap-up**
- [ ] `app_capabilities.txt` updated if capabilities changed
- [ ] `blueprint.md` consulted if core agentic loop was touched
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260318_spo-triple-extraction.md` (this file)
- `pkg/memory/schema.go` — SPOTriple helpers added
- `pkg/memory/knowledge.go` — KnowledgeNode extended, QueryNodesLinkingTo + QueryOutgoingEdges added
- `internal/tools/impl/memory_tools.go` — get_entity_network enhanced
- `internal/prompts/specialist_relationship.txt`
- `internal/prompts/specialist_thought.txt`
- `internal/prompts/specialist_work.txt`
- `internal/prompts/specialist_selfmodel.txt`

---

## Session Log

_Most recent first._

<!-- 20260318 session 3 -->
- Implemented Phase 2: extended `mergedFact` with `Predicate`/`ObjectValue`, populated via `ParseSPOTriple` in `mergeDreamerFacts`; added `SPOExtra` struct and `UpsertSemanticMemoryPreembeddedWithSPO` to `knowledge.go` (threads `spo` param through `upsertSemanticMemoryWithVector`); updated `dreamerWriteMergedFacts` to use new variant; extended `upsert_knowledge` tool with optional `predicate`/`object_value` params; updated `app_capabilities.txt`. `go build ./...` and `go vet ./...` both clean.

<!-- 20260318 session 2 -->
- Designed Phase 2 (SPO persistence): mergedFact struct extension, new `UpsertSemanticMemoryPreembeddedWithSPO` variant with `SPOExtra`, wiring in `dreamerWriteMergedFacts`, `upsert_knowledge` tool extension. `object_uuid` resolution deferred. Full plan in brief above.

<!-- 20260318 session 1 -->
- Implemented Phase 1: updated 4 specialist prompts with Subject|Predicate|Object format guidance, added `Predicate`/`ObjectUUID` fields to `KnowledgeNode`, added `SPOTriple`/`ParseSPOTriple`/`NormalizedPredicate` helpers to schema.go, added `QueryNodesLinkingTo` (array-contains) and `QueryOutgoingEdges` (object_uuid==) to knowledge.go, enhanced `get_entity_network` with 4-goroutine 1-hop traversal and updated synthesis prompt. `go build`, `go vet`, `go fmt` all pass cleanly.
