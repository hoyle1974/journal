# Graph Deduplication Design
**Date:** 2026-03-27
**Status:** Approved

## Problem

The knowledge graph has two deduplication gaps that cause "graph slop" over time:

1. **Entity fragmentation** — `EnsureNode` uses a SHA1 hash of the lowercased name as the doc ID. Semantic variants like "Sarah" and "Sarah Smith" hash to different IDs, creating siloed entity nodes. Relationships linked to one are invisible to queries on the other.

2. **Relationship ghost edges** — `CreateRelationshipNode` calls `generateUUID()` unconditionally. Every time the Refinery extracts the same triple (e.g. "Sarah works_at TechFlow"), a new relationship node is written. Over time, the graph accumulates hundreds of duplicate edges per entity pair, making `GraphExpand` exponentially noisier and more expensive.

## Scope

Two files in the `memory` package:
- `memory/knowledge_crud.go` — add `FindNearestByType` helper
- `memory/refinery_nodes.go` — revise `EnsureNode` and `CreateRelationshipNode`

One config file:
- `firestore.indexes.json` — add composite index for `(node_type, embedding)`

## Design

### 1. New helper: `FindNearestByType`

Add to `knowledge_crud.go`:

```go
func (s *Store) FindNearestByType(ctx context.Context, queryVector []float32, nodeType string, distanceThreshold float64) (*KnowledgeNode, error)
```

Chains `.Where("node_type", "==", nodeType)` before `FindNearest` in the Firestore query. Same return contract as `FindNearestWithThreshold` (returns `nil, nil` when no match is within threshold).

Requires a new composite index on `(node_type ASC, embedding VECTOR)` in `firestore.indexes.json`.

### 2. Revised `EnsureNode`

Updated flow:

1. Generate embedding for the identifier (always, upfront)
2. Call `FindNearestByType` with cosine distance threshold `0.08`
   - If a match is found: append `sourceEntryID` to its `journal_entry_ids` via `ArrayUnion`, return the matched node
3. If no match: run the existing transaction (SHA1 doc check → `name_key` exact match → create new node using the embedding already in hand)

The transaction remains the concurrency guard for the creation path. The vector search is the semantic dedup layer that intercepts before it. No second embedding call is needed on the creation path since the embedding was already generated in step 1.

### 3. Revised `CreateRelationshipNode`

Replace `generateUUID()` with a deterministic ID:

```
relID = "rel_" + hex(sha1(subjID + ":" + predicate + ":" + objID))
```

Full 40-char SHA1 hex — no truncation.

Updated flow:

1. Compute `relID` from the triple
2. Check if the doc already exists
   - If yes: append `sourceEntryID` via `ArrayUnion`, return existing `relID`
   - The caller (`refineryResolveCommit`) still runs `AddEntityLink` (idempotent) and `updateHotEdges` (bumps relevance score to 1.0 — desirable on re-observation)
3. If no: generate embedding, write new doc — same as today

## Data Model Impact

- Existing `rel_<uuid>` nodes become orphans (clean break accepted)
- New relationship nodes use `rel_<sha1>` prefix
- Entity nodes: no doc ID format change; semantic matches reuse the existing SHA1-keyed or name_key-matched doc

## Firestore Index

New composite index required in `firestore.indexes.json`:

```json
{
  "collectionGroup": "journal",
  "queryScope": "COLLECTION",
  "fields": [
    { "fieldPath": "node_type", "order": "ASCENDING" },
    { "fieldPath": "embedding", "vectorConfig": { "dimension": 768, "flat": {} } }
  ]
}
```

*(Dimension should match the project's configured embedding dimension.)*

## Out of Scope

- LLM-driven entity collision detection — deferred to v2 pending production metrics on graph fragmentation rates. A two-stage gate (auto-merge below 0.05, LLM validation between 0.05–0.08) was considered but rejected to keep the pipeline fast and deterministic. The `node_type` filter is the primary safeguard against false positives; the 0.08 threshold handles the rest conservatively.
- Migration of existing orphaned relationship nodes
- Changes to `UpsertKnowledge` or the FOH loop
