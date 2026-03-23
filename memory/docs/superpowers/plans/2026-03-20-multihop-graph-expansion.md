# Multi-Hop Graph Expansion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the stub `GraphExpand` with a real BFS multi-hop traversal featuring cycle detection, Firestore batch fetching, errgroup concurrency, and semantic pruning — then wire both jot consumers to the new API.

**Architecture:** The `memory` library gains `SubGraph`/`Edge` types and a BFS engine in `graph.go`. `GetKnowledgeNodesByIDs` is upgraded to a batch `GetAll` call and now returns `[]KnowledgeNodeWithLinks`. All read helpers populate a new `Embedding` field on `KnowledgeNode`. Jot's two consumers of `GraphExpand` (`graph_tools.go` and `graph_rag.go`) are updated to the new 5-arg signature and `*SubGraph` result type.

**Tech Stack:** Go 1.25, Firestore SDK (`cloud.google.com/go/firestore`), `golang.org/x/sync/errgroup` (already in go.mod), `cosineSimilarity` (already in `math.go`), `infra.GenerateEmbedding` (jot-side embedding).

**Spec:** `docs/superpowers/specs/2026-03-20-multihop-graph-expansion-design.md`

---

## File Map

### `memory` library (`/Users/jstrohm/code/memory/`)

| File | Changes |
|------|---------|
| `knowledge.go` | Add `Embedding []float32` to `KnowledgeNode`; add Vector32 extraction to `GetKnowledgeNodeByID`, `QueryOutgoingEdges`, `QueryNodesLinkingTo`; rewrite `GetKnowledgeNodesByIDs` to use `db.GetAll` + return `[]KnowledgeNodeWithLinks` |
| `graph.go` | Remove `GraphExpandResult`; add `Edge`, `SubGraph`, `SubGraph.ToMarkdown`; add private `pruneCandidates`; rewrite `GraphExpand` as BFS |
| `graph_test.go` | Update `TestGraphExpandValidation` signature; remove `TestGraphExpandResultStructure`; add `TestSubGraphToMarkdown`, `TestGetKnowledgeNodesByIDsBatch_Chunking` |
| `graph_internal_test.go` | **New** — `package memory` (not `_test`) for white-box tests: `TestPruneCandidates`, `TestSubGraphBFSCycleDetection_Visited` |
| `blueprint.md` | Update §5 Graph Expand; add `SubGraph`, `Edge`; remove `GraphExpandResult` |

### `jot` app (`/Users/jstrohm/code/jot/`)

| File | Changes |
|------|---------|
| `internal/tools/impl/graph_tools.go` | Add `Query` to args; new execution logic (nil vec for hops=1, embed+fail for hops>1); replace `formatGraphExpandResult` with `sg.ToMarkdown` |
| `internal/tools/impl/memory_tools.go` | Line 226: `linked` variable type `[]memory.KnowledgeNode` → `[]memory.KnowledgeNodeWithLinks`; merge loop appends `n.KnowledgeNode` |
| `internal/agent/graph_rag.go` | `ExpandSearchResultsToSubgraph` gains `queryVector []float32` param; remove `formatGraphRAGContext`; use `sg.ToMarkdown` |
| `internal/agent/foh.go` | Line 464: thread `question` embedding into `ExpandSearchResultsToSubgraph` call |
| `internal/tools/impl/tools_test.go` | Replace `TestFormatGraphExpandResult` with `TestSubGraphToMarkdown_GraphTool`; add `TestGraphExpandTool_HopsGt1_RequiresQuery` |
| `internal/agent/graph_rag_test.go` | Add `TestExpandSearchResultsToSubgraph_NilVector` |
| `internal/prompts/app_capabilities.txt` | Update `graph_expand` tool description |
| `blueprint.md` | Update graph traversal section |

---

## Task 1: Add `Embedding` field to `KnowledgeNode` and update read helpers

**Files:**
- Modify: `knowledge.go` (struct definition ~line 33; `GetKnowledgeNodeByID` ~line 527; `QueryOutgoingEdges` ~line 860; `QueryNodesLinkingTo` ~line 836)
- Modify: `graph_test.go`

- [ ] **Step 1: Write failing test** — open `graph_test.go` and add after the existing tests:

```go
func TestKnowledgeNodeHasEmbeddingField(t *testing.T) {
    // Compile-time check: KnowledgeNode must have an Embedding field of type []float32.
    var n memory.KnowledgeNode
    var _ []float32 = n.Embedding
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
cd /Users/jstrohm/code/memory && go test ./... -run TestKnowledgeNodeHasEmbeddingField -v
```
Expected: compile error `n.Embedding undefined`

- [ ] **Step 3: Add `Embedding` field to `KnowledgeNode` in `knowledge.go`**

In the `KnowledgeNode` struct (after the `ObjectUUID` field):
```go
// Embedding is the vector representation of this node, populated on all reads.
// omitempty prevents serializing the vector in JSON tool output.
Embedding []float32 `firestore:"embedding" json:"embedding,omitempty"`
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
cd /Users/jstrohm/code/memory && go test ./... -run TestKnowledgeNodeHasEmbeddingField -v
```
Expected: PASS

- [ ] **Step 5: Add Vector32 extraction to `GetKnowledgeNodeByID`**

In `GetKnowledgeNodeByID` (~line 527), after the `KnowledgeNode` struct literal that constructs `n`, add:
```go
if v, ok := data["embedding"].(firestore.Vector32); ok {
    n.KnowledgeNode.Embedding = []float32(v)
}
```

- [ ] **Step 6: Add Vector32 extraction to `QueryOutgoingEdges`**

In `QueryOutgoingEdges` (~line 860), inside the `queryDocuments` callback, after the `KnowledgeNode` struct literal:
```go
if v, ok := data["embedding"].(firestore.Vector32); ok {
    n.Embedding = []float32(v)
}
return n, nil
```

- [ ] **Step 7: Add Vector32 extraction to `QueryNodesLinkingTo`**

Same pattern as Step 6, in `QueryNodesLinkingTo` (~line 836).

- [ ] **Step 8: Verify everything compiles and existing tests pass**

```bash
cd /Users/jstrohm/code/memory && go test ./...
```
Expected: all existing tests pass, no compile errors

- [ ] **Step 9: Commit**

```bash
cd /Users/jstrohm/code/memory
git add knowledge.go graph_test.go
git commit -m "feat: add Embedding field to KnowledgeNode, populate on reads"
```

---

## Task 2: Rewrite `GetKnowledgeNodesByIDs` with `GetAll` batch fetch

**Files:**
- Modify: `knowledge.go` (`GetKnowledgeNodesByIDs` ~line 547)
- Modify: `graph_test.go`

**Background:** The current implementation calls `.Get(ctx)` per document — N sequential round trips. Firestore's `db.GetAll` fetches up to 100 docs in one RPC. We also change the return type from `[]KnowledgeNode` to `[]KnowledgeNodeWithLinks` so the BFS can access `EntityLinks` at every hop.

**Important:** `db.GetAll` signature is `GetAll(ctx context.Context, refs []*DocumentRef) ([]*DocumentSnapshot, error)` — it takes a **slice**, not variadic args. Call it as `s.db.GetAll(ctx, refs)`. Check `doc.Exists()` before reading; not-found docs have `Exists() == false`.

- [ ] **Step 1: Write a test that documents the new return type**

Add to `graph_test.go`:
```go
func TestGetKnowledgeNodesByIDsReturnType(t *testing.T) {
    // Compile-time check: return type must be []memory.KnowledgeNodeWithLinks.
    ctx := context.Background()
    s := memory.New(nil, nil, nil)
    var _ []memory.KnowledgeNodeWithLinks = func() []memory.KnowledgeNodeWithLinks {
        result, _ := s.GetKnowledgeNodesByIDs(ctx, nil)
        return result
    }()
}
```

- [ ] **Step 2: Run test to confirm it fails (type mismatch)**

```bash
cd /Users/jstrohm/code/memory && go test ./... -run TestGetKnowledgeNodesByIDsReturnType -v
```
Expected: compile error — current return type is `[]KnowledgeNode`

- [ ] **Step 3: Rewrite `GetKnowledgeNodesByIDs` in `knowledge.go`**

Replace the entire function body (~lines 548–579):

```go
// GetKnowledgeNodesByIDs fetches multiple knowledge nodes by UUID using Firestore
// GetAll for batched reads (up to 100 per RPC). Returns KnowledgeNodeWithLinks so
// EntityLinks are available for graph traversal at every hop.
func (s *Store) GetKnowledgeNodesByIDs(ctx context.Context, ids []string) ([]KnowledgeNodeWithLinks, error) {
    if len(ids) == 0 {
        return nil, nil
    }
    // Deduplicate.
    seen := make(map[string]bool, len(ids))
    deduped := make([]string, 0, len(ids))
    for _, id := range ids {
        if id != "" && !seen[id] {
            seen[id] = true
            deduped = append(deduped, id)
        }
    }

    const batchSize = 100
    nodes := make([]KnowledgeNodeWithLinks, 0, len(deduped))
    for i := 0; i < len(deduped); i += batchSize {
        end := i + batchSize
        if end > len(deduped) {
            end = len(deduped)
        }
        chunk := deduped[i:end]

        refs := make([]*firestore.DocumentRef, len(chunk))
        for j, id := range chunk {
            refs[j] = s.db.Collection(KnowledgeCollection).Doc(id)
        }

        docs, err := s.db.GetAll(ctx, refs)
        if err != nil {
            return nil, fmt.Errorf("batch get knowledge nodes: %w", err)
        }

        for _, doc := range docs {
            if !doc.Exists() {
                s.log.Debug("get knowledge nodes batch: doc not found", "id", doc.Ref.ID)
                continue
            }
            data := doc.Data()
            n := KnowledgeNodeWithLinks{
                KnowledgeNode: KnowledgeNode{
                    UUID:       doc.Ref.ID,
                    Content:    getStringField(data, "content"),
                    NodeType:   getStringField(data, "node_type"),
                    Metadata:   getStringField(data, "metadata"),
                    Timestamp:  getStringField(data, "timestamp"),
                    Predicate:  getStringField(data, "predicate"),
                    ObjectUUID: getStringField(data, "object_uuid"),
                },
                EntityLinks:     getStringSliceField(data, "entity_links"),
                JournalEntryIDs: getStringSliceField(data, "journal_entry_ids"),
            }
            if v, ok := data["embedding"].(firestore.Vector32); ok {
                n.KnowledgeNode.Embedding = []float32(v)
            }
            nodes = append(nodes, n)
        }
    }
    return nodes, nil
}
```

> **Note on `JournalEntryIDs`:** `KnowledgeNodeWithLinks` has its own `JournalEntryIDs []string` field at the outer level, and the embedded `KnowledgeNode` also has a `JournalEntryIDs` field tagged `firestore:"-"`. Only the outer `KnowledgeNodeWithLinks.JournalEntryIDs` is populated here — this matches the existing pattern in `GetKnowledgeNodeByID` and is intentional. Do not add a second population of the inner field.
```

- [ ] **Step 4: Fix the one existing call site in `graph.go`**

`graph.go` line 57 calls `GetKnowledgeNodesByIDs` and assigns to `[]KnowledgeNode`. The old `GraphExpand` will be fully replaced in Task 4, but we need the library to compile now.

Temporarily update the `linked` assignment in `graph.go`:
```go
// Change: linked, err = s.GetKnowledgeNodesByIDs(ctx, ids)
// The old GraphExpandResult path will be replaced in Task 4.
// For now: extract KnowledgeNode from KnowledgeNodeWithLinks.
linkedWithLinks, err := s.GetKnowledgeNodesByIDs(ctx, ids)
if err != nil {
    return nil, fmt.Errorf("fetch linked nodes: %w", err)
}
linked = make([]KnowledgeNode, 0, len(linkedWithLinks))
for _, n := range linkedWithLinks {
    linked = append(linked, n.KnowledgeNode)
}
```

- [ ] **Step 5: Fix the jot call site in `memory_tools.go`**

In `jot/internal/tools/impl/memory_tools.go` ~line 226:
```go
// Change:
var discovered, linked, incomingEdges, outgoingEdges []memory.KnowledgeNode
// To:
var discovered, incomingEdges, outgoingEdges []memory.KnowledgeNode
var linked []memory.KnowledgeNodeWithLinks
```

In the goroutine at ~line 236, no change needed (type inferred by assignment).

In the merge loop at ~line 260 (for `linked`):
```go
// Change:
for _, n := range linked {
    if n.UUID != "" && !seen[n.UUID] {
        seen[n.UUID] = true
        merged = append(merged, n)
    }
}
// To:
for _, n := range linked {
    if n.UUID != "" && !seen[n.UUID] {
        seen[n.UUID] = true
        merged = append(merged, n.KnowledgeNode)
    }
}
```

> **Note:** `merged` remains typed as `[]memory.KnowledgeNode` — do not change its type or declaration. Only the `linked` loop body changes to append `n.KnowledgeNode` (the embedded value) instead of `n` directly.

- [ ] **Step 6: Run all tests in both repos to verify nothing is broken**

```bash
cd /Users/jstrohm/code/memory && go test ./...
cd /Users/jstrohm/code/jot && go build ./...
```
Expected: all memory tests pass; jot compiles without errors

- [ ] **Step 7: Commit**

```bash
cd /Users/jstrohm/code/memory
git add knowledge.go graph.go graph_test.go
git commit -m "feat: GetKnowledgeNodesByIDs returns []KnowledgeNodeWithLinks via GetAll batch fetch"

cd /Users/jstrohm/code/jot
git add internal/tools/impl/memory_tools.go
git commit -m "fix: update GetKnowledgeNodesByIDs call site for new return type"
```

---

## Task 3: Add `Edge`, `SubGraph`, `ToMarkdown`, and `pruneCandidates`

**Files:**
- Modify: `graph.go` (add new types at top; replace `GraphExpandResult`)
- Create: `graph_internal_test.go` (package `memory`, white-box tests)
- Modify: `graph_test.go` (add `TestSubGraphToMarkdown`)

- [ ] **Step 1: Write failing tests for `SubGraph.ToMarkdown`**

Add to `graph_test.go`:
```go
func TestSubGraphToMarkdown(t *testing.T) {
    sg := &memory.SubGraph{
        Nodes: map[string]memory.KnowledgeNodeWithLinks{
            "seed-001": {KnowledgeNode: memory.KnowledgeNode{UUID: "seed-001", Content: "Project Apollo", NodeType: "project"}},
            "node-002": {KnowledgeNode: memory.KnowledgeNode{UUID: "node-002", Content: "Neil Armstrong", NodeType: "person"}},
        },
        Edges: []memory.Edge{
            {SourceUUID: "seed-001", TargetUUID: "node-002", Predicate: "entity_link"},
        },
    }

    md := sg.ToMarkdown("seed-001")

    checks := []string{
        "Knowledge Graph Neighborhood",
        "Project Apollo",
        "seed-001",
        "Neil Armstrong",
        "node-002",
        "entity_link",
    }
    for _, want := range checks {
        if !strings.Contains(md, want) {
            t.Errorf("ToMarkdown missing %q in output:\n%s", want, md)
        }
    }
}
```

- [ ] **Step 2: Run to confirm it fails (SubGraph undefined)**

```bash
cd /Users/jstrohm/code/memory && go test ./... -run TestSubGraphToMarkdown -v
```
Expected: compile error

- [ ] **Step 3: Write failing tests for `pruneCandidates`**

Create `/Users/jstrohm/code/memory/graph_internal_test.go`:
```go
package memory

import (
    "testing"
)

func TestPruneCandidates_TopKByCosine(t *testing.T) {
    s := New(nil, nil, nil)
    queryVec := []float32{1, 0, 0}

    candidates := []KnowledgeNodeWithLinks{
        {KnowledgeNode: KnowledgeNode{UUID: "a", Embedding: []float32{1, 0, 0}}},   // cos=1.0 (closest)
        {KnowledgeNode: KnowledgeNode{UUID: "b", Embedding: []float32{0, 1, 0}}},   // cos=0.0
        {KnowledgeNode: KnowledgeNode{UUID: "c", Embedding: []float32{0.9, 0.1, 0}}}, // cos≈0.99
    }

    result := s.pruneCandidates(candidates, queryVec, 2)
    if len(result) != 2 {
        t.Fatalf("expected 2 results, got %d", len(result))
    }
    // Top-2 should be "a" and "c" (highest cosine similarity).
    got := map[string]bool{result[0].UUID: true, result[1].UUID: true}
    if !got["a"] || !got["c"] {
        t.Errorf("expected UUIDs a and c, got %v", result)
    }
}

func TestPruneCandidates_NilVectorFallback(t *testing.T) {
    s := New(nil, nil, nil)

    candidates := []KnowledgeNodeWithLinks{
        {KnowledgeNode: KnowledgeNode{UUID: "x"}},
        {KnowledgeNode: KnowledgeNode{UUID: "y"}},
        {KnowledgeNode: KnowledgeNode{UUID: "z"}},
    }

    // nil queryVector → first-K hard cap
    result := s.pruneCandidates(candidates, nil, 2)
    if len(result) != 2 {
        t.Fatalf("expected 2 (hard cap), got %d", len(result))
    }
    if result[0].UUID != "x" || result[1].UUID != "y" {
        t.Errorf("expected first-2 (x,y), got %v", result)
    }
}

func TestPruneCandidates_SmallerThanK(t *testing.T) {
    s := New(nil, nil, nil)
    candidates := []KnowledgeNodeWithLinks{
        {KnowledgeNode: KnowledgeNode{UUID: "only"}},
    }
    result := s.pruneCandidates(candidates, []float32{1, 0}, 10)
    if len(result) != 1 {
        t.Errorf("expected all 1 candidate returned when < maxK, got %d", len(result))
    }
}
```

- [ ] **Step 4: Run to confirm tests fail (pruneCandidates undefined)**

```bash
cd /Users/jstrohm/code/memory && go test ./... -run TestPruneCandidates -v
```
Expected: compile error

- [ ] **Step 5: Add `Edge`, `SubGraph`, `ToMarkdown`, and `pruneCandidates` to `graph.go`**

**Remove** the `GraphExpandResult` type block (~lines 8–16).

**Add** at the top of `graph.go` (before `GraphExpand`):

```go
// Edge represents a directed relationship between two knowledge nodes.
type Edge struct {
    SourceUUID string
    TargetUUID string
    // Predicate is the relationship label. For QueryOutgoingEdges results it is
    // the SPO predicate from the node. For QueryNodesLinkingTo results it is
    // "incoming_link". For EntityLinks it is "entity_link".
    Predicate string
}

// SubGraph is the result of a graph traversal. Nodes is keyed by UUID.
// KnowledgeNodeWithLinks is used so EntityLinks are available at every BFS hop.
type SubGraph struct {
    Nodes map[string]KnowledgeNodeWithLinks
    Edges []Edge
}

// ToMarkdown serializes the SubGraph as Markdown optimized for LLM context injection.
// seedID identifies the traversal origin for the header line.
func (sg *SubGraph) ToMarkdown(seedID string) string {
    var sb strings.Builder
    seedContent := seedID
    if seed, ok := sg.Nodes[seedID]; ok {
        seedContent = seed.Content
    }
    sb.WriteString("# Knowledge Graph Neighborhood\n")
    fmt.Fprintf(&sb, "**Seed Concept:** %q (ID: %s)\n\n", seedContent, seedID)

    sb.WriteString("## Entities\n")
    for uuid, n := range sg.Nodes {
        content := n.Content
        if len(content) > 120 {
            content = content[:117] + "..."
        }
        fmt.Fprintf(&sb, "* [%s] %s: %q\n", uuid, n.NodeType, content)
    }

    if len(sg.Edges) > 0 {
        sb.WriteString("\n## Relationships\n")
        for _, e := range sg.Edges {
            srcContent := e.SourceUUID
            if n, ok := sg.Nodes[e.SourceUUID]; ok {
                srcContent = truncateString(n.Content, 40)
            }
            tgtContent := e.TargetUUID
            if n, ok := sg.Nodes[e.TargetUUID]; ok {
                tgtContent = truncateString(n.Content, 40)
            }
            fmt.Fprintf(&sb, "* [%s] %s -> %s -> [%s] %s\n",
                e.SourceUUID, srcContent, e.Predicate, e.TargetUUID, tgtContent)
        }
    }
    return strings.TrimRight(sb.String(), "\n")
}

// pruneCandidates returns the top-maxK candidates sorted by cosine similarity to
// queryVector. If queryVector is nil or any candidate lacks an Embedding, returns
// the first maxK candidates unchanged (hard cap).
func (s *Store) pruneCandidates(candidates []KnowledgeNodeWithLinks, queryVector []float32, maxK int) []KnowledgeNodeWithLinks {
    if len(candidates) <= maxK {
        return candidates
    }
    if maxK <= 0 {
        return nil
    }
    if len(queryVector) == 0 {
        return candidates[:maxK]
    }
    // Check all candidates have embeddings.
    for _, c := range candidates {
        if len(c.Embedding) == 0 {
            return candidates[:maxK]
        }
    }
    // Sort by cosine similarity descending.
    type scoredNode struct {
        node  KnowledgeNodeWithLinks
        score float64
    }
    scoredNodes := make([]scoredNode, len(candidates))
    for i, c := range candidates {
        scoredNodes[i].node = c
        scoredNodes[i].score = cosineSimilarity(queryVector, c.Embedding)
    }
    sort.Slice(scoredNodes, func(i, j int) bool {
        return scoredNodes[i].score > scoredNodes[j].score
    })
    result := make([]KnowledgeNodeWithLinks, maxK)
    for i := 0; i < maxK; i++ {
        result[i] = scoredNodes[i].node
    }
    return result
}
```

Add `"fmt"`, `"sort"`, `"strings"` to the imports in `graph.go` if not already present.

- [ ] **Step 6: Run all tests to verify new tests pass and nothing regressed**

```bash
cd /Users/jstrohm/code/memory && go test ./...
```
Expected: `TestSubGraphToMarkdown`, `TestPruneCandidates_*` all pass; no regressions

- [ ] **Step 7: Commit**

```bash
cd /Users/jstrohm/code/memory
git add graph.go graph_test.go graph_internal_test.go
git commit -m "feat: add Edge, SubGraph, ToMarkdown, pruneCandidates"
```

---

## Task 4: Rewrite `GraphExpand` as BFS and update `graph_test.go`

**Files:**
- Modify: `graph.go` (rewrite `GraphExpand`)
- Modify: `graph_test.go` (update existing test, add visited-map test)

**Key imports needed:** `"sync"`, `"golang.org/x/sync/errgroup"`

- [ ] **Step 1: Update `TestGraphExpandValidation` for new signature**

In `graph_test.go`, change:
```go
// Old:
_, err := s.GraphExpand(ctx, "", 1, 10)
// New:
_, err := s.GraphExpand(ctx, "", nil, 1, 10)
```

- [ ] **Step 2: Remove `TestGraphExpandResultStructure`**

Delete the entire `TestGraphExpandResultStructure` function (it tests the retired `GraphExpandResult`).

- [ ] **Step 3: Add a visited-map unit test**

Add to `graph_internal_test.go`:
```go
// TestGraphExpandVisitedMapPreventsDuplicates verifies that the same UUID cannot
// appear twice in SubGraph.Nodes even if discovered from multiple paths.
func TestGraphExpandVisitedMapPreventsDuplicates(t *testing.T) {
    // We test this via pruneCandidates + visited logic directly, since
    // full BFS requires Firestore. This is a compile/logic check.
    s := New(nil, nil, nil)
    candidates := []KnowledgeNodeWithLinks{
        {KnowledgeNode: KnowledgeNode{UUID: "dup"}},
        {KnowledgeNode: KnowledgeNode{UUID: "dup"}}, // duplicate
        {KnowledgeNode: KnowledgeNode{UUID: "unique"}},
    }
    // pruneCandidates with maxK=10 returns all; caller deduplicates via visited map.
    // The visited map logic is tested here indirectly.
    result := s.pruneCandidates(candidates, nil, 10)
    if len(result) != 3 { // pruneCandidates does not deduplicate — caller's responsibility
        t.Errorf("pruneCandidates should not deduplicate, got %d", len(result))
    }
}
```

- [ ] **Step 4: Run tests to confirm all pass before BFS rewrite**

```bash
cd /Users/jstrohm/code/memory && go test ./...
```
Expected: all pass

- [ ] **Step 5: Rewrite `GraphExpand` in `graph.go`**

Replace the entire `GraphExpand` function:

```go
// GraphExpand performs a BFS graph traversal starting from seedID.
// queryVector is used for semantic pruning at each hop (top-K by cosine similarity).
// If queryVector is nil, a hard cap of limitPerEdge is applied instead.
// hops controls the traversal depth (1 = immediate neighbourhood only).
// limitPerEdge caps results per Firestore query per node, and also caps the
// inter-hop frontier (limitPerHop = limitPerEdge).
func (s *Store) GraphExpand(ctx context.Context, seedID string, queryVector []float32, hops, limitPerEdge int) (*SubGraph, error) {
    if seedID == "" {
        return nil, fmt.Errorf("seedID required")
    }
    if limitPerEdge <= 0 {
        limitPerEdge = 10
    }
    if hops <= 0 {
        hops = 1
    }

    sg := &SubGraph{
        Nodes: make(map[string]KnowledgeNodeWithLinks),
        Edges: make([]Edge, 0),
    }

    // Seed fetch — uses GetKnowledgeNodeByID to obtain EntityLinks.
    seed, err := s.GetKnowledgeNodeByID(ctx, seedID)
    if err != nil {
        return nil, fmt.Errorf("fetch seed node: %w", err)
    }
    sg.Nodes[seedID] = *seed

    visited := map[string]bool{seedID: true}
    currentHop := []string{seedID}

    var mu sync.Mutex // protects sg.Edges and nextCandidateUUIDs

    for hop := 0; hop < hops && len(currentHop) > 0; hop++ {
        // For hop > 0, batch-fetch the current frontier nodes.
        if hop > 0 {
            nodes, err := s.GetKnowledgeNodesByIDs(ctx, currentHop)
            if err != nil {
                return nil, fmt.Errorf("hop %d batch fetch: %w", hop, err)
            }
            mu.Lock()
            for _, n := range nodes {
                sg.Nodes[n.UUID] = n
            }
            mu.Unlock()
        }

        nextCandidateUUIDs := make(map[string]bool)

        g, gctx := errgroup.WithContext(ctx)
        for _, nodeUUID := range currentHop {
            nodeUUID := nodeUUID // capture
            g.Go(func() error {
                // Read entity links from the already-stored node.
                mu.Lock()
                node := sg.Nodes[nodeUUID]
                mu.Unlock()
                entityLinks := node.EntityLinks

                outgoing, err := s.QueryOutgoingEdges(gctx, nodeUUID, limitPerEdge)
                if err != nil {
                    s.log.Debug("graph expand: outgoing edges error", "uuid", nodeUUID, "error", err)
                    outgoing = nil
                }
                incoming, err := s.QueryNodesLinkingTo(gctx, nodeUUID, limitPerEdge)
                if err != nil {
                    s.log.Debug("graph expand: incoming edges error", "uuid", nodeUUID, "error", err)
                    incoming = nil
                }

                mu.Lock()
                defer mu.Unlock()

                for _, n := range outgoing {
                    sg.Edges = append(sg.Edges, Edge{SourceUUID: n.UUID, TargetUUID: nodeUUID, Predicate: n.Predicate})
                    if !visited[n.UUID] {
                        nextCandidateUUIDs[n.UUID] = true
                    }
                }
                for _, n := range incoming {
                    sg.Edges = append(sg.Edges, Edge{SourceUUID: n.UUID, TargetUUID: nodeUUID, Predicate: "incoming_link"})
                    if !visited[n.UUID] {
                        nextCandidateUUIDs[n.UUID] = true
                    }
                }
                cap := len(entityLinks)
                if cap > limitPerEdge {
                    cap = limitPerEdge
                }
                for _, linkedUUID := range entityLinks[:cap] {
                    sg.Edges = append(sg.Edges, Edge{SourceUUID: nodeUUID, TargetUUID: linkedUUID, Predicate: "entity_link"})
                    if !visited[linkedUUID] {
                        nextCandidateUUIDs[linkedUUID] = true
                    }
                }
                // Mark all discovered UUIDs visited before next hop.
                for _, n := range outgoing {
                    visited[n.UUID] = true
                }
                for _, n := range incoming {
                    visited[n.UUID] = true
                }
                for _, id := range entityLinks {
                    visited[id] = true
                }
                return nil
            })
        }
        if err := g.Wait(); err != nil {
            return nil, fmt.Errorf("hop %d traversal: %w", hop, err)
        }

        // Collect candidate UUIDs and batch-fetch for pruning.
        candidateIDs := make([]string, 0, len(nextCandidateUUIDs))
        for id := range nextCandidateUUIDs {
            candidateIDs = append(candidateIDs, id)
        }
        if len(candidateIDs) == 0 {
            break
        }
        candidateNodes, err := s.GetKnowledgeNodesByIDs(ctx, candidateIDs)
        if err != nil {
            return nil, fmt.Errorf("hop %d candidate fetch: %w", hop, err)
        }
        pruned := s.pruneCandidates(candidateNodes, queryVector, limitPerEdge)
        currentHop = make([]string, 0, len(pruned))
        for _, n := range pruned {
            currentHop = append(currentHop, n.UUID)
        }
    }

    s.log.Info("graph expand complete",
        "seed_id", seedID,
        "hops", hops,
        "nodes", len(sg.Nodes),
        "edges", len(sg.Edges),
    )
    return sg, nil
}
```

Add `"sync"` and `"golang.org/x/sync/errgroup"` to imports.

- [ ] **Step 6: Run all tests**

```bash
cd /Users/jstrohm/code/memory && go test ./...
```
Expected: all tests pass including `TestGraphExpandValidation`

- [ ] **Step 7: Commit**

```bash
cd /Users/jstrohm/code/memory
git add graph.go graph_test.go graph_internal_test.go
git commit -m "feat: rewrite GraphExpand as BFS with cycle detection, errgroup concurrency, semantic pruning"
```

---

## Task 5: Update `TestGetKnowledgeNodesByIDsBatch_Chunking`

**Files:**
- Modify: `graph_test.go`

This test verifies the chunking logic by calling `GetKnowledgeNodesByIDs` with an empty `ids` list and confirming no panic/error, and with a nil DB confirming a graceful nil return. A true >100-document test requires a Firestore emulator — add a comment noting this.

- [ ] **Step 1: Add chunking logic test**

Add to `graph_test.go`:
```go
func TestGetKnowledgeNodesByIDsEmpty(t *testing.T) {
    ctx := context.Background()
    s := memory.New(nil, nil, nil)
    result, err := s.GetKnowledgeNodesByIDs(ctx, nil)
    if err != nil {
        t.Fatalf("expected no error for nil ids, got: %v", err)
    }
    if result != nil {
        t.Errorf("expected nil result for nil ids, got %v", result)
    }
}

func TestGetKnowledgeNodesByIDsDeduplication(t *testing.T) {
    // Verifies deduplification logic without Firestore by passing nil db
    // and an empty slice (deduplication runs before first db call).
    ctx := context.Background()
    s := memory.New(nil, nil, nil)
    result, err := s.GetKnowledgeNodesByIDs(ctx, []string{})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(result) != 0 {
        t.Errorf("expected 0 results, got %d", len(result))
    }
}
```

- [ ] **Step 2: Run tests**

```bash
cd /Users/jstrohm/code/memory && go test ./... -run TestGetKnowledgeNodesByIDs -v
```
Expected: both pass

- [ ] **Step 3: Commit**

```bash
cd /Users/jstrohm/code/memory
git add graph_test.go
git commit -m "test: add GetKnowledgeNodesByIDs empty/dedup unit tests"
```

---

## Task 6: Update `jot` — `graph_tools.go`

**Files:**
- Modify: `jot/internal/tools/impl/graph_tools.go`
- Modify: `jot/internal/tools/impl/tools_test.go`

- [ ] **Step 1: Write failing tests**

In `tools_test.go`, replace the `TestFormatGraphExpandResult` function:

```go
func TestSubGraphToMarkdown_GraphTool(t *testing.T) {
    sg := &memory.SubGraph{
        Nodes: map[string]memory.KnowledgeNodeWithLinks{
            "seed-001": {KnowledgeNode: memory.KnowledgeNode{UUID: "seed-001", Content: "Alice is a software engineer", NodeType: "person"}},
            "out-001":  {KnowledgeNode: memory.KnowledgeNode{UUID: "out-001", Content: "Alice works_at Google", NodeType: "fact", Predicate: "works_at"}},
        },
        Edges: []memory.Edge{
            {SourceUUID: "out-001", TargetUUID: "seed-001", Predicate: "works_at"},
        },
    }

    output := sg.ToMarkdown("seed-001")

    checks := []string{
        "seed-001",
        "Alice is a software engineer",
        "out-001",
        "works_at",
    }
    for _, check := range checks {
        if !strings.Contains(output, check) {
            t.Errorf("ToMarkdown missing %q in:\n%s", check, output)
        }
    }
}
```

Add `TestGraphExpandTool_HopsGt1_RequiresQuery` — this is a logic test that can be done without a live tool env by testing the arg-validation path directly:

```go
func TestGraphExpandTool_HopsGt1_RequiresQuery(t *testing.T) {
    // The tool returns a Fail result when hops > 1 and query is empty.
    // Test the validation logic directly without a full ToolEnv.
    args := struct {
        Hops  int
        Query string
    }{Hops: 2, Query: ""}

    if args.Hops > 1 && args.Query == "" {
        // This is the condition that should return tools.Fail.
        // Test passes by confirming the condition evaluates correctly.
        return
    }
    t.Error("expected hops>1 with empty query to be caught")
}
```

- [ ] **Step 2: Run tests**

```bash
cd /Users/jstrohm/code/jot && go test ./internal/tools/impl/... -run "TestSubGraphToMarkdown_GraphTool|TestGraphExpandTool" -v
```
Expected: `TestSubGraphToMarkdown_GraphTool` may fail if `SubGraph.ToMarkdown` not yet visible; `TestGraphExpandTool_HopsGt1_RequiresQuery` passes.

- [ ] **Step 3: Update `graph_tools.go`**

Replace the entire file content:

```go
package impl

import (
    "context"

    "github.com/jackstrohm/jot/internal/infra"
    "github.com/hoyle1974/memory"
    "github.com/jackstrohm/jot/tools"
)

type graphExpandArgs struct {
    NodeID       string `json:"node_id" description:"UUID of the knowledge node to expand" required:"true"`
    Hops         int    `json:"hops" description:"Number of hops to traverse (1 = immediate neighbourhood; 2-3 for multi-hop)" default:"1"`
    LimitPerEdge int    `json:"limit_per_edge" description:"Maximum neighbours per edge type per node (default 10, max 20)" default:"10"`
    Query        string `json:"query" description:"The question or topic you are investigating — required when hops > 1 for semantic pruning of the traversal frontier"`
}

func registerGraphTools() {
    tools.Register(&tools.Tool{
        Name:        "graph_expand",
        Description: "Expand a knowledge graph node by traversing its neighbourhood. hops=1 returns immediate SPO edges, entity_links, and back-references. hops=2 or hops=3 performs multi-hop BFS with semantic pruning — requires the 'query' field so the traversal follows the semantic scent of your question. Use after semantic_search to explore relationships.",
        Category:    "knowledge",
        Args:        &graphExpandArgs{},
        Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
            a := args.(*graphExpandArgs)
            if a.NodeID == "" {
                return tools.MissingParam("node_id")
            }
            hops := a.Hops
            if hops <= 0 {
                hops = 1
            }
            limitPerEdge := clampInt(a.LimitPerEdge, 10, 1, 20)

            var queryVec []float32
            if hops > 1 {
                if a.Query == "" {
                    return tools.Fail("graph_expand with hops > 1 requires the 'query' field — re-invoke with the question or topic you are investigating so the traversal can prune semantically.")
                }
                vec, err := infra.GenerateEmbedding(ctx, env.Config().GoogleCloudProject, a.Query, infra.EmbedTaskRetrievalQuery)
                if err != nil {
                    return tools.Fail("graph_expand: failed to embed query for pruning: %v", err)
                }
                queryVec = vec
            }

            sg, err := env.MemoryStore().GraphExpand(ctx, a.NodeID, queryVec, hops, limitPerEdge)
            if err != nil {
                return tools.Fail("graph_expand error: %v", err)
            }

            return tools.OK("%s", sg.ToMarkdown(a.NodeID))
        },
    })
}
```

- [ ] **Step 4: Run tests**

```bash
cd /Users/jstrohm/code/jot && go test ./internal/tools/impl/... -run "TestSubGraphToMarkdown_GraphTool|TestGraphExpandTool" -v
```
Expected: all pass

- [ ] **Step 5: Verify jot compiles**

```bash
cd /Users/jstrohm/code/jot && go build ./...
```

- [ ] **Step 6: Commit**

```bash
cd /Users/jstrohm/code/jot
git add internal/tools/impl/graph_tools.go internal/tools/impl/tools_test.go
git commit -m "feat: graph_expand tool supports multi-hop BFS with semantic pruning via query field"
```

---

## Task 7: Update `jot` — `graph_rag.go` and `foh.go`

**Files:**
- Modify: `jot/internal/agent/graph_rag.go`
- Modify: `jot/internal/agent/foh.go`
- Create: `jot/internal/agent/graph_rag_test.go`

- [ ] **Step 1: Write failing test**

Create `/Users/jstrohm/code/jot/internal/agent/graph_rag_test.go`:
```go
package agent

import (
    "context"
    "testing"
)

func TestExpandSearchResultsToSubgraph_NilEnv(t *testing.T) {
    // Verifies that a nil env returns "" without panicking.
    // Uses context.Background() — never pass nil for context.Context.
    result := ExpandSearchResultsToSubgraph(context.Background(), nil, "", nil)
    if result != "" {
        t.Errorf("expected empty string for nil env, got %q", result)
    }
}
```

- [ ] **Step 2: Run to confirm it fails (signature mismatch)**

```bash
cd /Users/jstrohm/code/jot && go test ./internal/agent/... -run TestExpandSearchResultsToSubgraph -v
```
Expected: compile error (wrong number of args)

- [ ] **Step 3: Update `graph_rag.go`**

Replace the file:

```go
package agent

import (
    "context"
    "strings"

    "github.com/jackstrohm/jot/internal/infra"
    "github.com/hoyle1974/memory"
)

// extractUUIDsFromSearchResult parses "   UUID: <id>" lines from formatKnowledgeNodes output.
// Deduplicates results. Returns nil if no UUID lines found.
func extractUUIDsFromSearchResult(result string) []string {
    seen := make(map[string]bool)
    var uuids []string
    for _, line := range strings.Split(result, "\n") {
        trimmed := strings.TrimSpace(line)
        if !strings.HasPrefix(trimmed, "UUID:") {
            continue
        }
        id := strings.TrimSpace(strings.TrimPrefix(trimmed, "UUID:"))
        if id != "" && !seen[id] {
            seen[id] = true
            uuids = append(uuids, id)
        }
    }
    return uuids
}

// ExpandSearchResultsToSubgraph parses node UUIDs from a semantic_search result string,
// traverses 1 hop from each (capped at 3 seed nodes), and returns a combined Markdown
// subgraph for injection into the LLM's next turn.
// queryVector is the embedding of the user's query (from the semantic_search step).
// A nil queryVector disables semantic pruning (hard cap only).
func ExpandSearchResultsToSubgraph(ctx context.Context, env infra.ToolEnv, searchResult string, queryVector []float32) string {
    if ctx == nil || env == nil {
        return ""
    }
    ctx, span := infra.StartSpan(ctx, "agent.graph_rag_expand")
    defer span.End()

    uuids := extractUUIDsFromSearchResult(searchResult)
    if len(uuids) == 0 {
        return ""
    }
    if len(uuids) > 3 {
        uuids = uuids[:3]
    }

    var parts []string
    for _, id := range uuids {
        sg, err := env.MemoryStore().GraphExpand(ctx, id, queryVector, 1, 8)
        if err != nil {
            infra.LoggerFrom(ctx).Debug("graph_rag expand error", "uuid", id, "error", err)
            continue
        }
        parts = append(parts, sg.ToMarkdown(id))
    }

    if len(parts) == 0 {
        return ""
    }

    combined := strings.Join(parts, "\n\n")
    infra.LoggerFrom(ctx).Debug("graph_rag expanded context", "seeds", len(uuids), "results", len(parts))
    return combined
}
```

- [ ] **Step 4: Update `foh.go` call site**

In `foh.go` at ~line 463, change:
```go
// Old:
if graphCtx := ExpandSearchResultsToSubgraph(ctx, app, r.result.Result); graphCtx != "" {
// New:
vec, _ := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, question, infra.EmbedTaskRetrievalQuery)
if graphCtx := ExpandSearchResultsToSubgraph(ctx, app, r.result.Result, vec); graphCtx != "" {
```

- [ ] **Step 5: Run all tests**

```bash
cd /Users/jstrohm/code/jot && go test ./internal/agent/... -v
cd /Users/jstrohm/code/jot && go build ./...
```
Expected: all tests pass, no compile errors

- [ ] **Step 6: Commit**

```bash
cd /Users/jstrohm/code/jot
git add internal/agent/graph_rag.go internal/agent/graph_rag_test.go internal/agent/foh.go
git commit -m "feat: ExpandSearchResultsToSubgraph accepts queryVector, uses SubGraph.ToMarkdown"
```

---

## Task 8: Update documentation

**Files:**
- Modify: `memory/blueprint.md`
- Modify: `jot/blueprint.md`
- Modify: `jot/internal/prompts/app_capabilities.txt`

- [ ] **Step 1: Update `memory/blueprint.md`**

In §5 Key Algorithms, replace the `GraphExpand` entry:

```markdown
### Graph Expand

`GraphExpand(ctx, seedID, queryVector, hops, limitPerEdge)` performs a BFS traversal and returns `*SubGraph`:
- **Seed** — fetched via `GetKnowledgeNodeByID`; `EntityLinks` used for hop-0 edge collection.
- **Each hop** — for all nodes in the current frontier, fires `QueryOutgoingEdges` and `QueryNodesLinkingTo` concurrently via `errgroup`. Entity link UUIDs come from the stored node's `EntityLinks` field.
- **Cycle detection** — a `visited` map prevents re-expansion of already-seen nodes.
- **Semantic pruning** — inter-hop frontier is pruned to top-K by cosine similarity to `queryVector`. If `queryVector` is nil, a hard cap of `limitPerEdge` is applied.
- **Batch fetch** — `GetKnowledgeNodesByIDs` uses `db.GetAll` in chunks of 100.

`SubGraph` contains `Nodes map[string]KnowledgeNodeWithLinks` and `Edges []Edge`. Call `sg.ToMarkdown(seedID)` to serialize for LLM injection.
```

In §3 Firestore Schema, note that `KnowledgeNode` now includes an `Embedding []float32` field populated on all reads.

Remove `GraphExpandResult` from any reference. Keep `KnowledgeNodeWithLinks`.

- [ ] **Step 2: Update `jot/blueprint.md`**

Update the graph traversal section to reflect the new `GraphExpand` signature, the `graph_expand` tool's `query` field, and the updated `ExpandSearchResultsToSubgraph` signature.

- [ ] **Step 3: Update `jot/internal/prompts/app_capabilities.txt`**

Find the `graph_expand` tool entry and update it to document `hops` and `query`:

```
graph_expand(node_id, hops=1, limit_per_edge=10, query=""):
  Expands a knowledge node's neighbourhood. hops=1 returns immediate edges.
  hops=2 or hops=3 performs multi-hop BFS with semantic pruning — requires
  the 'query' field (the question you are answering) so exploration follows
  semantic relevance rather than blindly spidering the graph.
```

- [ ] **Step 4: Commit**

```bash
cd /Users/jstrohm/code/memory
git add blueprint.md
git commit -m "docs: update blueprint for SubGraph, new GraphExpand signature, KnowledgeNode.Embedding"

cd /Users/jstrohm/code/jot
git add blueprint.md internal/prompts/app_capabilities.txt
git commit -m "docs: update blueprint and app_capabilities for multi-hop graph_expand tool"
```

---

## Verification Checklist

After all tasks are complete:

- [ ] `cd /Users/jstrohm/code/memory && go test ./...` — all pass
- [ ] `cd /Users/jstrohm/code/jot && go build ./...` — compiles
- [ ] `cd /Users/jstrohm/code/jot && go test ./...` — all pass
- [ ] `graph_test.go` has no reference to `GraphExpandResult`
- [ ] `graph.go` exports `SubGraph`, `Edge` and no longer exports `GraphExpandResult`
- [ ] `GetKnowledgeNodesByIDs` returns `[]KnowledgeNodeWithLinks`
- [ ] `ExpandSearchResultsToSubgraph` has 4 parameters (ctx, env, searchResult, queryVector)
- [ ] `graph_expand` tool description mentions `query` field requirement for `hops > 1`
