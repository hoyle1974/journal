# Multi-Hop Graph Expansion Design

**Date:** 2026-03-20
**Repos affected:** `github.com/hoyle1974/memory`, `github.com/jackstrohm/jot`
**Status:** Approved for implementation

---

## 1. Problem

`GraphExpand` in `graph.go` accepts a `hops` parameter that is a stub — only 1-hop traversal is implemented. Multi-hop graph traversal on Firestore faces a branching-factor explosion: a node with 10 edges produces O(b^d) candidate documents at depth d. Without cycle detection, bidirectional links cause infinite loops. Without semantic pruning, the result set grows too large for Claude's context window and degrades reasoning quality.

---

## 2. Goals

- Implement true multi-hop BFS traversal with cycle detection.
- Bound the result set with semantic pruning (cosine similarity against the query vector) so only the most relevant nodes are explored at each hop.
- Reduce Firestore latency via batch document fetches and concurrent per-node edge queries.
- Produce a `SubGraph` result type with a `ToMarkdown` serializer optimized for LLM consumption.
- Update both jot consumers of `GraphExpand` to the new API.
- Replace the retired `GraphExpandResult` type cleanly.

---

## 3. Non-Goals

- No new Firestore indexes are required.
- No changes to node types, schema, or the janitor.
- No changes to the `HybridSearch` / RAG pipeline beyond the `KnowledgeNode` embedding field addition.

---

## 4. Data Structure Changes

### 4.1 `KnowledgeNode` — add `Embedding` field

Add `Embedding []float32` to the struct:

```go
// New field — populated on all reads for semantic pruning.
// omitempty prevents bloating JSON tool output.
Embedding []float32 `firestore:"embedding" json:"embedding,omitempty"`
```

**Deserialization:** All read helpers use the manual `doc.Data()` map-extraction pattern (not `doc.DataTo()`). This pattern must be preserved — switching to `doc.DataTo()` would break `UUID` (tagged `firestore:"-"`) and `JournalEntryIDs` (also tagged `firestore:"-"`, populated manually via `getStringSliceField`).

To extract the embedding from a raw `doc.Data()` map, use:
```go
if v, ok := data["embedding"].(firestore.Vector32); ok {
    node.Embedding = []float32(v)
}
```

Apply this extraction in: `GetKnowledgeNodeByID`, `GetKnowledgeNodesByIDs`, `QueryOutgoingEdges`, `QueryNodesLinkingTo`, and any other helper that constructs a `KnowledgeNode` from a Firestore document.

### 4.2 New types in `graph.go`

```go
// Edge represents a directed relationship between two knowledge nodes.
type Edge struct {
    SourceUUID string
    TargetUUID string
    // Predicate is the relationship label: the actual SPO predicate for outgoing
    // edges, "incoming_link" for entity_link back-references, or "entity_link"
    // for direct entity_links entries.
    Predicate string
}

// SubGraph is the result of a multi-hop graph traversal. Nodes is keyed by UUID.
// KnowledgeNodeWithLinks is used (not KnowledgeNode) so EntityLinks are available
// at every hop of the BFS without a separate Firestore read.
type SubGraph struct {
    Nodes map[string]KnowledgeNodeWithLinks
    Edges []Edge
}

// ToMarkdown serializes the SubGraph as a Markdown document optimized for
// LLM context injection. seedID identifies the traversal origin for the header.
func (sg *SubGraph) ToMarkdown(seedID string) string
```

`ToMarkdown` output structure:
```
# Knowledge Graph Neighborhood
**Seed Concept:** "<content>" (ID: <seedID>)

## Entities
* [<uuid>] <NodeType>: "<Content>"
...

## Relationships
* [<src>] <src-content-short> -> <predicate> -> [<tgt>] <tgt-content-short>
...
```

### 4.3 Retire `GraphExpandResult`; retain `KnowledgeNodeWithLinks`

`GraphExpandResult` is **removed**. `KnowledgeNodeWithLinks` is **retained and promoted** — the BFS needs `EntityLinks` for every node at every hop, not just the seed. `SubGraph.Nodes` is therefore `map[string]KnowledgeNodeWithLinks`.

**`GetKnowledgeNodeByID` signature is unchanged** — it continues to return `*KnowledgeNodeWithLinks`. Its callers (`UpdateProjectStatus`, `isCompletedProjectByID`) are unaffected.

**`GetKnowledgeNodesByIDs` return type changes** from `[]KnowledgeNode` to `[]KnowledgeNodeWithLinks`. The batch-fetch implementation (§5.1) must also extract `entity_links` via `getStringSliceField(data, "entity_links")` on each document. All call sites that currently iterate `[]KnowledgeNode` are updated to `[]KnowledgeNodeWithLinks`.

> **`KnowledgeNodeWithLinks` gains `Embedding` indirectly** — it embeds `KnowledgeNode` by value, so the new `Embedding []float32` field is automatically available without a separate change to the struct.

---

## 5. `memory` Library Changes

### 5.1 `GetKnowledgeNodesByIDs` — batch fetch

**Signature changes** from `[]KnowledgeNode` to `[]KnowledgeNodeWithLinks` (see §4.3). Internal implementation is rewritten to use `db.GetAll(ctx, refs...)` with chunking at 100 documents per call (Firestore's `GetAll` limit). This reduces N sequential round trips to `ceil(N/100)` parallel batch requests.

```go
// Pseudocode
func (s *Store) GetKnowledgeNodesByIDs(ctx context.Context, ids []string) ([]KnowledgeNodeWithLinks, error) {
    // deduplicate ids
    // chunk into groups of 100
    // for each chunk: build []*firestore.DocumentRef, call db.GetAll(ctx, refs...)
    // assemble results: skip docs where doc.Exists() == false (not-found)
    // for each doc: extract content/node_type/metadata/timestamp/predicate/object_uuid
    //               extract EntityLinks via getStringSliceField(data, "entity_links")
    //               extract Embedding via firestore.Vector32 cast (see §4.1)
}
```

### 5.2 `GraphExpand` — new signature

```go
func (s *Store) GraphExpand(
    ctx context.Context,
    seedID string,
    queryVector []float32, // nil = no semantic pruning, hard cap only
    hops int,
    limitPerEdge int,
) (*SubGraph, error)
```

### 5.3 BFS Algorithm

**Edge direction conventions** (critical for correct `nextHop` collection):

| Query | What it returns | Edge direction | UUID added to `nextHopSet` |
|-------|----------------|----------------|---------------------------|
| `QueryIncomingSPOEdges(nodeUUID)` | Nodes where `object_uuid == nodeUUID` — SPO triples where `node` is the **object** (incoming SPO edges) | `Edge{SourceUUID: returnedNode.UUID, TargetUUID: nodeUUID, Predicate: returnedNode.Predicate}` | `returnedNode.UUID` |
| `QueryNodesLinkingTo(nodeUUID)` | Nodes where `entity_links` contains `nodeUUID` | `Edge{SourceUUID: returnedNode.UUID, TargetUUID: nodeUUID, Predicate: "incoming_link"}` | `returnedNode.UUID` |
| `node.ObjectUUID` | The UUID this node points to as its SPO object (intrinsic outgoing SPO edge) | `Edge{SourceUUID: nodeUUID, TargetUUID: node.ObjectUUID, Predicate: node.Predicate}` | `node.ObjectUUID` |
| `node.EntityLinks` | UUIDs directly listed in `node.entity_links` | `Edge{SourceUUID: nodeUUID, TargetUUID: linkedUUID, Predicate: "entity_link"}` | `linkedUUID` |

```
visited    = {seedID: true}
sg         = SubGraph{Nodes: {}, Edges: []}
currentHop = [seedID]

// Seed fetch: use GetKnowledgeNodeByID to get EntityLinks for hop-0
seed = GetKnowledgeNodeByID(seedID)          // returns *KnowledgeNodeWithLinks
sg.Nodes[seedID] = *seed                    // stored as KnowledgeNodeWithLinks

for hop in 0..hops-1:
    if hop > 0:
        nodes = GetKnowledgeNodesByIDs(currentHop)   // returns []KnowledgeNodeWithLinks
        add nodes to sg.Nodes

    nextHopCandidateUUIDs = {}  // thread-safe set, protected by mutex

    for each nodeUUID in currentHop (concurrently via errgroup):
        entityLinks = sg.Nodes[nodeUUID].EntityLinks  // EntityLinks available on all hops
        incomingSPO = QueryIncomingSPOEdges(nodeUUID, limitPerEdge)
        incoming    = QueryNodesLinkingTo(nodeUUID, limitPerEdge)
        // Note: do NOT fetch entityLinks via GetKnowledgeNodesByIDs here.
        // The UUIDs are sufficient for nextHopCandidateUUIDs; resolved nodes
        // are fetched in the per-hop batch at the end (candidateNodes step below).

        if node.ObjectUUID != "":
            register Edge{nodeUUID, node.ObjectUUID, node.Predicate}
            if !visited[node.ObjectUUID]: nextHopCandidateUUIDs.add(node.ObjectUUID)
        for each n in incomingSPO:
            register Edge{n.UUID, nodeUUID, n.Predicate}
            if !visited[n.UUID]: nextHopCandidateUUIDs.add(n.UUID)
        for each n in incoming:
            register Edge{n.UUID, nodeUUID, "incoming_link"}
            if !visited[n.UUID]: nextHopCandidateUUIDs.add(n.UUID)
        for each uuid in entityLinks:
            register Edge{nodeUUID, uuid, "entity_link"}
            if !visited[uuid]: nextHopCandidateUUIDs.add(uuid)

        mark all discovered UUIDs as visited

    candidateNodes = GetKnowledgeNodesByIDs(nextHopCandidateUUIDs)
    currentHop     = UUIDsOf(pruneCandidates(candidateNodes, queryVector, limitPerHop))

return sg
```

**Cycle detection:** `visited` is checked before adding any UUID to `nextHopCandidateUUIDs`. All UUIDs discovered during a hop (regardless of whether they pass the pruning step) are marked visited before the next hop begins, preventing re-expansion.

**Concurrency:** The three edge queries per node are fired concurrently inside a single `errgroup`. A mutex protects the shared `nextHopCandidateUUIDs` set and `sg.Edges` slice.

### 5.4 `pruneCandidates` — private method

```go
func (s *Store) pruneCandidates(candidates []KnowledgeNodeWithLinks, queryVector []float32, maxK int) []KnowledgeNodeWithLinks
```

The `maxK` parameter here is a **per-hop limit** (`limitPerHop`), not `limitPerEdge`. This is intentional: the inter-hop prune caps the total next-hop frontier regardless of how many edges produced it. A hop with 5 nodes × 3 edge types × 10 results each = 150 candidates is pruned to `limitPerHop` (default: same value as `limitPerEdge`, e.g. 10). This guarantees the BFS frontier stays bounded across hops.

- If `len(candidates) <= maxK`: return as-is.
- If `queryVector == nil` or any candidate has empty `Embedding`: return first-K (hard cap).
- Otherwise: sort by `cosineSimilarity(queryVector, node.Embedding)` descending, return top-K.

### 5.5 `blueprint.md` update (same commit)

- Update `GraphExpand` entry in §5 Key Algorithms to reflect new signature, BFS algorithm, and `SubGraph`/`Edge` types.
- Add `SubGraph`, `Edge` to the exported types table.
- Remove `GraphExpandResult`. Keep `KnowledgeNodeWithLinks` (still used by `GetKnowledgeNodeByID`).

---

## 6. `jot` Changes

### 6.1 `internal/tools/impl/graph_tools.go`

Updated args struct:
```go
type graphExpandArgs struct {
    NodeID       string `json:"node_id" required:"true"`
    Hops         int    `json:"hops" default:"1"`
    LimitPerEdge int    `json:"limit_per_edge" default:"10"`
    Query        string `json:"query" description:"The question or topic you are investigating — required when hops > 1 for semantic pruning"`
}
```

Execution logic:
- `hops == 1`: call `GraphExpand(ctx, nodeID, nil, 1, limitPerEdge)`
- `hops > 1` and `query == ""`: return `tools.Fail` with message asking LLM to re-invoke with `query` populated
- `hops > 1` and `query != ""`: generate embedding via `infra.GenerateEmbedding(ctx, env.Config().GoogleCloudProject, query, infra.EmbedTaskRetrievalQuery)`, then call `GraphExpand(ctx, nodeID, vec, hops, limitPerEdge)`

This follows the same embedding pattern used by `memory_tools.go` (e.g. `semantic_search`).

`formatGraphExpandResult` is removed; output is `sg.ToMarkdown(nodeID)`.

Tool description updated to document multi-hop capability and the `query` requirement.

### 6.2 `internal/agent/graph_rag.go`

Updated signature:
```go
func ExpandSearchResultsToSubgraph(
    ctx context.Context,
    env infra.ToolEnv,
    searchResult string,
    queryVector []float32,
) string
```

- Calls `GraphExpand(ctx, id, queryVector, 1, 8)` per seed UUID (unchanged hop count).
- `formatGraphRAGContext` is removed; output is `sg.ToMarkdown(id)`.

**Call site in `foh.go`:** The sole caller is at line 464:
```go
if r.fcName == "semantic_search" && r.result.Success {
    if graphCtx := ExpandSearchResultsToSubgraph(ctx, app, r.result.Result); graphCtx != "" {
```

Update this call to:
```go
if r.fcName == "semantic_search" && r.result.Success {
    vec, _ := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, question, infra.EmbedTaskRetrievalQuery)
    if graphCtx := ExpandSearchResultsToSubgraph(ctx, app, r.result.Result, vec); graphCtx != "" {
```

`question` is the FOH loop's outer `question string` parameter, available throughout the loop body. If the embedding call fails, `vec` will be nil — `ExpandSearchResultsToSubgraph` treats nil as no pruning (hard cap only), which is safe.

### 6.3 Documentation (same commit)

- `internal/prompts/app_capabilities.txt`: update `graph_expand` tool description to mention multi-hop and `query` field.
- `blueprint.md`: update graph traversal section.

---

## 7. Testing

### `memory/graph_test.go`

| Test | Action | What it verifies |
|------|--------|-----------------|
| `TestGraphExpandValidation` | Update call to `s.GraphExpand(ctx, "", nil, 1, 10)` | Empty seedID still returns error with new 5-arg signature |
| `TestGraphExpandResultStructure` | **Remove** | Tests retired `GraphExpandResult` |
| `TestSubGraphToMarkdown` | **New** (table-driven) | `ToMarkdown` output contains seed header, entity entries, relationship lines for various graph shapes |
| `TestPruneCandidates` | **New** | Top-K selection by cosine similarity; nil-vector fallback returns first-K; nodes with empty `Embedding` fall back to first-K |
| `TestSubGraphBFSCycleDetection` | **New** | Bidirectional A↔B link does not cause infinite loop; both nodes appear exactly once in `sg.Nodes` |
| `TestGetKnowledgeNodesByIDsBatch` | **New** | >100 IDs are chunked; each chunk of 100 issues one `GetAll` call; results from all chunks are combined |

### `jot/internal/tools/impl/tools_test.go`

| Test | Action | What it verifies |
|------|--------|-----------------|
| `TestFormatGraphExpandResult` | **Replace** with `TestSubGraphToMarkdown_GraphTool` | Tool output from `sg.ToMarkdown` contains seed UUID, node content, edge predicates |
| `TestGraphExpandTool_HopsGt1_RequiresQuery` | **New** | `hops=2` with no `query` arg returns `tools.Fail` result |

### `jot/internal/agent/graph_rag_test.go`

| Test | Action | What it verifies |
|------|--------|-----------------|
| `TestExtractUUIDsFromSearchResult` | No change | Existing UUID parsing behavior |
| `TestExpandSearchResultsToSubgraph_PassesVector` | **New** | `queryVector` arg is accepted; nil vector produces valid (non-empty) output when nodes exist |

---

## 8. Migration Notes

- `GraphExpandResult` is removed. `KnowledgeNodeWithLinks` is **retained and promoted** (used in `SubGraph.Nodes` and returned by both `GetKnowledgeNodeByID` and the updated `GetKnowledgeNodesByIDs`).
- `GetKnowledgeNodesByIDs` return type changes from `[]KnowledgeNode` to `[]KnowledgeNodeWithLinks`. All call sites (in both `memory` and `jot`) must be updated.
- There are exactly two call sites for `GraphExpand` in jot; both are updated in the same PR.
- The `Embedding` field on `KnowledgeNode` is populated on all reads after this change. Callers that previously ignored embeddings are unaffected.
- No Firestore index changes required.
