# Graph Traversal & Graph RAG Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add BFS subgraph traversal to `pkg/memory` and wire it into the FOH query loop so that a `semantic_search` hit automatically expands 1-2 degrees to return a richer context block injected into the LLM's next turn.

**Architecture:** Add `TraverseSubgraph` (BFS over `entity_links`, `object_uuid`, and incoming edges) and `FormatSubgraph` (text block for LLM injection) to `pkg/memory/graph.go`. Modify `formatKnowledgeNodes` in `internal/tools/impl/helpers.go` to include `UUID: <id>` lines so the FOH and the `graph_expand` tool can parse node UUIDs from search results. Register a `graph_expand` tool. Add `graph_rag.go` in `internal/agent/` with `ExpandSearchResultsToSubgraph`, and wire it into `foh.go` after `wg.Wait()` by injecting the subgraph block as an additional `*genai.Part` in the `functionResponses` slice.

**Tech Stack:** Go, Firestore (existing `entity_links` + `object_uuid` fields), `pkg/memory`, `internal/agent/foh.go`, `internal/tools/impl/memory_tools.go`, `internal/tools/impl/helpers.go`

---

## Context: What Already Exists

Before touching anything, read these files and understand their contents:

- `pkg/memory/knowledge.go` — `GetKnowledgeNodeByID` (line 597), `GetKnowledgeNodesByIDs` (624), `QueryNodesLinkingTo` (967), `AddEntityLink` (360), `KnowledgeNodeWithLinks` struct with `EntityLinks []string` and `ObjectUUID` field on `KnowledgeNode`
- `internal/tools/impl/helpers.go` — `formatKnowledgeNodes` formats search results as numbered text without UUIDs (this is the gap we fix in Task 1)
- `internal/tools/impl/memory_tools.go` — has one `init()` at line 45 that calls `registerKnowledgeTools()` and `registerSignalTools()`; new tools must follow this pattern
- `internal/agent/foh.go` — the `toolExecResult` struct (line 386) and `results []toolExecResult` after `wg.Wait()` (line 434); `functionResponses []*genai.Part` (line 436); the correct way to append is `&genai.Part{FunctionResponse: &genai.FunctionResponse{...}}`
- `pkg/memory/schema.go` — `SPOTriple`, `ParseSPOTriple`, `NormalizedPredicate` already defined

The key insight: `semantic_search` currently returns text without UUIDs, so there is no way to hook graph traversal from the text output. Fix: add `UUID: <id>` lines to `formatKnowledgeNodes` so UUIDs are present in the result string, then parse them in Graph RAG.

---

## File Map

| Action | File | Responsibility |
|---|---|---|
| **Create** | `pkg/memory/graph.go` | `TraverseSubgraph` BFS + `FormatSubgraph` text formatter |
| **Create** | `pkg/memory/graph_test.go` | Unit tests for FormatSubgraph and input validation |
| **Modify** | `internal/tools/impl/helpers.go` | Add `UUID: <id>` line per node in `formatKnowledgeNodes` |
| **Modify** | `internal/tools/impl/memory_tools.go` | Add `registerGraphTools()` + `graph_expand` tool |
| **Create** | `internal/agent/graph_rag.go` | `extractUUIDsFromSearchResult` + `ExpandSearchResultsToSubgraph` |
| **Create** | `internal/agent/graph_rag_test.go` | Tests for UUID extraction and subgraph formatting |
| **Modify** | `internal/agent/foh.go` | Wire graph expansion after semantic_search results |
| **Modify** | `internal/prompts/app_capabilities.txt` | Document new tool and Graph RAG behavior |
| **Modify** | `blueprint.md` | Update FOH pipeline description |

---

## Task 1: `TraverseSubgraph` and `FormatSubgraph` in `pkg/memory/graph.go`

**Files:**
- Create: `pkg/memory/graph.go`
- Create: `pkg/memory/graph_test.go`

### What `TraverseSubgraph` does

BFS from `rootUUID` following three edge types per level:
1. Outgoing `EntityLinks` (from `KnowledgeNodeWithLinks.EntityLinks`)
2. SPO object edge (`KnowledgeNode.ObjectUUID`)
3. Incoming edges via `QueryNodesLinkingTo` (nodes that reference this UUID in their `entity_links`)

Cap at 50 nodes total. Hard cap depth at 3.

- [ ] **Step 1: Write the failing tests**

```go
// pkg/memory/graph_test.go
package memory_test

import (
	"strings"
	"testing"

	"github.com/jackstrohm/jot/pkg/memory"
)

func TestTraverseSubgraph_EmptyRoot(t *testing.T) {
	ctx := t.Context()
	// Empty rootUUID must return error, not panic.
	_, err := memory.TraverseSubgraph(ctx, nil, "", 1)
	if err == nil {
		t.Fatal("expected error for empty rootUUID, got nil")
	}
}

func TestTraverseSubgraph_NilEnvNonEmptyRoot(t *testing.T) {
	ctx := t.Context()
	// Non-empty rootUUID with nil env must return an error, not panic.
	_, err := memory.TraverseSubgraph(ctx, nil, "some-uuid", 1)
	if err == nil {
		t.Fatal("expected error for nil env with non-empty rootUUID, got nil")
	}
}

func TestFormatSubgraph_Empty(t *testing.T) {
	result := memory.FormatSubgraph(nil)
	if result != "" {
		t.Fatalf("expected empty string for nil nodes, got %q", result)
	}
}

func TestFormatSubgraph_Single(t *testing.T) {
	nodes := []memory.KnowledgeNode{
		{UUID: "abc", NodeType: "person", Content: "Gloria is Jeff's wife"},
	}
	out := memory.FormatSubgraph(nodes)
	if out == "" {
		t.Fatal("expected non-empty output for single node")
	}
	if !strings.Contains(out, "Gloria") {
		t.Fatalf("expected content in output, got %q", out)
	}
	if !strings.Contains(out, "person") {
		t.Fatalf("expected node_type in output, got %q", out)
	}
}

func TestFormatSubgraph_SPONode(t *testing.T) {
	nodes := []memory.KnowledgeNode{
		{UUID: "spo1", NodeType: "generic", Content: "Gloria is_wife_of Jeff", Predicate: "is_wife_of", ObjectUUID: "jeff-uuid"},
	}
	out := memory.FormatSubgraph(nodes)
	if !strings.Contains(out, "is_wife_of") {
		t.Fatalf("expected predicate in output, got %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/jstrohm/code/jot
go test ./pkg/memory/... -run "TestTraverseSubgraph|TestFormatSubgraph" -v
```

Expected: compile error — `memory.TraverseSubgraph` and `memory.FormatSubgraph` not defined.

- [ ] **Step 3: Implement `pkg/memory/graph.go`**

```go
package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
)

const maxSubgraphNodes = 50

// TraverseSubgraph performs BFS from rootUUID following entity_links, object_uuid (SPO),
// and incoming edges (QueryNodesLinkingTo) up to depth hops. Returns a flat, deduplicated
// slice of all nodes found (including root). Capped at maxSubgraphNodes.
// depth=1 returns root + immediate neighbors. depth=2 includes neighbors-of-neighbors.
// Hard cap: depth is clamped to [1, 3] to prevent runaway reads.
//
// Performance note: each BFS level makes O(N) Firestore reads where N is the frontier size,
// plus one QueryNodesLinkingTo per frontier node. For depth=2 with fanout=10, expect ~30 reads.
// This is acceptable for v1; future optimization: replace GetKnowledgeNodesByIDs with GetAll batch read.
func TraverseSubgraph(ctx context.Context, env infra.ToolEnv, rootUUID string, depth int) ([]KnowledgeNode, error) {
	if rootUUID == "" {
		return nil, fmt.Errorf("rootUUID is required")
	}
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 3 {
		depth = 3
	}

	ctx, span := infra.StartSpan(ctx, "memory.traverse_subgraph")
	defer span.End()

	seen := make(map[string]bool)
	var result []KnowledgeNode

	// Load root node.
	root, err := GetKnowledgeNodeByID(ctx, env, rootUUID)
	if err != nil {
		return nil, fmt.Errorf("load root node %s: %w", rootUUID, err)
	}
	seen[rootUUID] = true
	result = append(result, root.KnowledgeNode)

	frontier := []KnowledgeNodeWithLinks{*root}

	for d := 0; d < depth && len(result) < maxSubgraphNodes; d++ {
		var nextFrontierIDs []string

		for _, node := range frontier {
			// Outgoing entity_links.
			for _, id := range node.EntityLinks {
				if id != "" && !seen[id] {
					seen[id] = true
					nextFrontierIDs = append(nextFrontierIDs, id)
				}
			}
			// SPO object edge.
			if node.ObjectUUID != "" && !seen[node.ObjectUUID] {
				seen[node.ObjectUUID] = true
				nextFrontierIDs = append(nextFrontierIDs, node.ObjectUUID)
			}
			// Incoming edges: nodes whose entity_links contain this UUID.
			incoming, err := QueryNodesLinkingTo(ctx, env, node.UUID, 10)
			if err != nil {
				infra.LoggerFrom(ctx).Debug("graph_traverse incoming edge error", "node", node.UUID, "error", err)
			}
			for _, n := range incoming {
				if !seen[n.UUID] {
					seen[n.UUID] = true
					nextFrontierIDs = append(nextFrontierIDs, n.UUID)
				}
			}
		}

		if len(nextFrontierIDs) == 0 {
			break
		}

		// Batch load next frontier nodes.
		nextNodes, err := GetKnowledgeNodesByIDs(ctx, env, nextFrontierIDs)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("graph_traverse batch load error", "error", err)
			break
		}

		// Build next frontier for BFS (load with links for edge traversal).
		frontier = make([]KnowledgeNodeWithLinks, 0, len(nextNodes))
		for _, n := range nextNodes {
			if len(result) >= maxSubgraphNodes {
				break
			}
			result = append(result, n)
			full, err := GetKnowledgeNodeByID(ctx, env, n.UUID)
			if err == nil {
				frontier = append(frontier, *full)
			}
		}
	}

	infra.LoggerFrom(ctx).Debug("graph_traverse complete", "root", rootUUID, "depth", depth, "nodes_found", len(result))
	return result, nil
}

// FormatSubgraph renders a slice of nodes as a compact text block for LLM context injection.
// Returns empty string if nodes is nil or empty.
func FormatSubgraph(nodes []KnowledgeNode) string {
	if len(nodes) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("GRAPH CONTEXT:\n")
	for _, n := range nodes {
		if n.Predicate != "" && n.ObjectUUID != "" {
			sb.WriteString(fmt.Sprintf("[%s] %s (%s → %s)\n", n.NodeType, n.Content, n.Predicate, n.ObjectUUID))
		} else {
			sb.WriteString(fmt.Sprintf("[%s] %s\n", n.NodeType, n.Content))
		}
	}
	return sb.String()
}
```

Note on `FormatSubgraph`: this is a data formatter, not a multi-source prompt template — `fmt.Sprintf` is acceptable here. The project's no-`fmt.Sprintf` rule applies to parameterized *prompt assembly* (system prompts, LLM instructions assembled from multiple inputs). `FormatSubgraph` formats structured data into a string using simple patterns.

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /Users/jstrohm/code/jot
go test ./pkg/memory/... -run "TestTraverseSubgraph|TestFormatSubgraph" -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Build check**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add pkg/memory/graph.go pkg/memory/graph_test.go
git commit -m "feat(memory): add TraverseSubgraph BFS and FormatSubgraph for graph RAG"
```

---

## Task 2: Add `UUID:` lines to `formatKnowledgeNodes` in `helpers.go`

**Files:**
- Modify: `internal/tools/impl/helpers.go`

This is the linchpin for Graph RAG: without UUIDs in the search output, the FOH cannot identify which nodes to expand. Adding `UUID: <id>` lines also lets the LLM use `graph_expand` explicitly.

- [ ] **Step 1: Read `helpers.go` first**

Read `internal/tools/impl/helpers.go` fully. Find `formatKnowledgeNodes` (it formats each node as a numbered list entry). Understand the current format before changing it.

- [ ] **Step 2: Write the failing test**

In `internal/tools/impl/tools_test.go` (or `helpers_test.go` if it exists), add:

```go
func TestFormatKnowledgeNodes_ContainsUUID(t *testing.T) {
	// This test verifies UUIDs are present in formatted output so graph_rag can parse them.
	// Adjust the helper call to whatever formatKnowledgeNodes' signature is after reading the file.
	nodes := []memory.KnowledgeNode{
		{UUID: "test-uuid-123", NodeType: "person", Content: "Gloria is Jeff's wife", Timestamp: "2026-01-01"},
	}
	out := formatKnowledgeNodesForTest(nodes) // rename to match the actual func or export it
	if !strings.Contains(out, "test-uuid-123") {
		t.Fatalf("expected UUID in formatted output, got:\n%s", out)
	}
}
```

Note: `formatKnowledgeNodes` may be unexported. If so, either: (a) make it exported as `FormatKnowledgeNodes` (rename), or (b) test it indirectly through the tool result. Read the file to decide.

- [ ] **Step 3: Modify `formatKnowledgeNodes` to include UUID lines**

After reading the file, find the per-node formatting block. Add a `UUID: <uuid>` line immediately after the node header line. The output should look like:

```
1. [person] [2026-01-01] Gloria is Jeff's wife
   UUID: abc-uuid-123
   Metadata: {...}
```

This line must be exactly `   UUID: <uuid>` (3 spaces indent + "UUID: ") so `extractUUIDsFromSearchResult` can reliably parse it.

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/tools/... -run TestFormatKnowledgeNodes -v
```

Expected: PASS.

- [ ] **Step 5: Build and run all tool tests**

```bash
go build ./...
go test ./internal/tools/... -v
```

Expected: no failures. The LLM now sees UUID lines in search results, which is additive information.

- [ ] **Step 6: Commit**

```bash
git add internal/tools/impl/helpers.go internal/tools/impl/tools_test.go
git commit -m "feat(tools): include UUID lines in formatKnowledgeNodes output for graph RAG"
```

---

## Task 3: `graph_expand` tool in `memory_tools.go`

**Files:**
- Modify: `internal/tools/impl/memory_tools.go`
- Modify: `internal/tools/impl/impl_test.go`

Follow the existing registration pattern: add a `registerGraphTools()` function and call it from the existing `init()`. Do NOT add a second `init()` block.

- [ ] **Step 1: Write the failing test**

In `internal/tools/impl/impl_test.go`, add `"graph_expand"` to the `registeredTools` list:

```go
// Find the existing list of expected tool names and add:
"graph_expand",
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/tools/... -run TestRegisteredTools -v
```

Expected: FAIL — `graph_expand` not registered.

- [ ] **Step 3: Add `registerGraphTools` to `memory_tools.go`**

```go
type graphExpandArgs struct {
	NodeUUID string `json:"node_uuid" description:"UUID of the root knowledge node to expand outward" required:"true"`
	Depth    int    `json:"depth" description:"Traversal depth: 1 (immediate neighbors) or 2 (two hops). Default 1." default:"1"`
}

func registerGraphTools() {
	tools.Register(&tools.Tool{
		Name:        "graph_expand",
		Description: "Expand a known knowledge node UUID outward 1-2 hops in the graph, returning all connected entities, facts, and relationships. Use after semantic_search when you have a UUID from search results (look for 'UUID: <id>' lines) and want richer context about related concepts.",
		Category:    "knowledge",
		Args:        &graphExpandArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			ctx, span := infra.StartSpan(ctx, "tool.graph_expand")
			defer span.End()

			a := args.(*graphExpandArgs)
			if strings.TrimSpace(a.NodeUUID) == "" {
				return tools.Fail("node_uuid cannot be empty")
			}
			depth := a.Depth
			if depth < 1 || depth > 2 {
				depth = 1
			}
			nodes, err := memory.TraverseSubgraph(ctx, env, a.NodeUUID, depth)
			if err != nil {
				return tools.Fail("graph expand failed: %v", err)
			}
			if len(nodes) == 0 {
				return tools.OK("No connected nodes found for UUID %s", a.NodeUUID)
			}
			return tools.OK(memory.FormatSubgraph(nodes))
		},
	})
}
```

Then in the existing `init()`:

```go
func init() {
	registerKnowledgeTools()
	registerSignalTools()
	registerGraphTools() // add this line
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/tools/... -run TestRegisteredTools -v
```

Expected: PASS.

- [ ] **Step 5: Build check**

```bash
go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add internal/tools/impl/memory_tools.go internal/tools/impl/impl_test.go
git commit -m "feat(tools): register graph_expand tool via registerGraphTools()"
```

---

## Task 4: `graph_rag.go` — Graph RAG auto-expansion

**Files:**
- Create: `internal/agent/graph_rag.go`
- Create: `internal/agent/graph_rag_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/agent/graph_rag_test.go
package agent

import (
	"strings"
	"testing"
)

func TestExtractUUIDsFromSearchResult_Valid(t *testing.T) {
	// formatKnowledgeNodes now emits "   UUID: <id>" (3-space indent).
	input := "1. [person] [2026-01-01] Jeff\n   UUID: abc-123\n\n2. [place] [2026-01-01] The Park\n   UUID: def-456\n"
	got := extractUUIDsFromSearchResult(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 UUIDs, got %d: %v", len(got), got)
	}
	if got[0] != "abc-123" {
		t.Errorf("expected abc-123, got %q", got[0])
	}
	if got[1] != "def-456" {
		t.Errorf("expected def-456, got %q", got[1])
	}
}

func TestExtractUUIDsFromSearchResult_NoUUIDs(t *testing.T) {
	input := "1. [person] [2026-01-01] Jeff — no UUID line here"
	got := extractUUIDsFromSearchResult(input)
	if len(got) != 0 {
		t.Fatalf("expected 0 UUIDs, got %d: %v", len(got), got)
	}
}

func TestExtractUUIDsFromSearchResult_Empty(t *testing.T) {
	got := extractUUIDsFromSearchResult("")
	if len(got) != 0 {
		t.Fatalf("expected 0 UUIDs for empty input, got %d", len(got))
	}
}

func TestExtractUUIDsFromSearchResult_Dedup(t *testing.T) {
	// Duplicate UUIDs should not be returned twice.
	input := "   UUID: abc-123\n   UUID: abc-123\n"
	got := extractUUIDsFromSearchResult(input)
	if len(got) != 1 {
		t.Fatalf("expected 1 unique UUID, got %d: %v", len(got), got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/agent/... -run TestExtractUUIDs -v
```

Expected: compile error — `extractUUIDsFromSearchResult` not defined.

- [ ] **Step 3: Implement `internal/agent/graph_rag.go`**

```go
package agent

import (
	"context"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/memory"
)

// extractUUIDsFromSearchResult parses "   UUID: <id>" lines from formatKnowledgeNodes output.
// Deduplicates UUIDs. Returns nil if no UUID lines are found.
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

// ExpandSearchResultsToSubgraph takes a semantic_search result string, extracts node UUIDs,
// traverses 1 hop from each (capped at 3 seed nodes), and returns a formatted subgraph block
// for injection into the LLM's context. Returns empty string if no UUIDs found or all traversals fail.
func ExpandSearchResultsToSubgraph(ctx context.Context, env infra.ToolEnv, searchResult string) string {
	ctx, span := infra.StartSpan(ctx, "agent.graph_rag_expand")
	defer span.End()

	uuids := extractUUIDsFromSearchResult(searchResult)
	if len(uuids) == 0 {
		return ""
	}
	// Cap seed nodes to limit Firestore read fanout.
	if len(uuids) > 3 {
		uuids = uuids[:3]
	}

	seen := make(map[string]bool)
	var allNodes []memory.KnowledgeNode

	for _, id := range uuids {
		nodes, err := memory.TraverseSubgraph(ctx, env, id, 1)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("graph_rag expand error", "uuid", id, "error", err)
			continue
		}
		for _, n := range nodes {
			if !seen[n.UUID] {
				seen[n.UUID] = true
				allNodes = append(allNodes, n)
			}
		}
	}

	if len(allNodes) == 0 {
		return ""
	}

	infra.LoggerFrom(ctx).Debug("graph_rag expanded context", "seed_count", len(uuids), "total_nodes", len(allNodes))
	return memory.FormatSubgraph(allNodes)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/agent/... -run TestExtractUUIDs -v
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Build check**

```bash
go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add internal/agent/graph_rag.go internal/agent/graph_rag_test.go
git commit -m "feat(agent): add graph_rag.go with extractUUIDsFromSearchResult and ExpandSearchResultsToSubgraph"
```

---

## Task 5: Wire Graph RAG into `foh.go`

**Files:**
- Modify: `internal/agent/foh.go`

**Read `internal/agent/foh.go` first.** The integration point is after `wg.Wait()` (line ~434) and before building `functionResponses`. Specifically, in the loop that iterates over `results []toolExecResult` (starting around line 445):

```go
for _, r := range results {
    // ... logging ...
    functionResponses = append(functionResponses, &genai.Part{
        FunctionResponse: &genai.FunctionResponse{
            Name:     r.fcName,
            Response: map[string]any{"result": utils.SanitizePrompt(r.result.Result)},
        },
    })
    // ...
}
```

After each `semantic_search` result is added to `functionResponses`, run Graph RAG and inject the subgraph as an additional part.

- [ ] **Step 1: Locate the exact insertion point**

Read lines 434–470 of `foh.go`. Find the `for _, r := range results` loop and the `functionResponses = append(...)` line. The `app` variable (of type `*infra.App`) is the `env` to pass to `ExpandSearchResultsToSubgraph` — verify `*infra.App` satisfies `infra.ToolEnv` by checking `internal/infra/app.go` for the `Config()` and `Firestore()` method receivers.

- [ ] **Step 2: Add graph expansion inside the results loop**

After the `functionResponses = append(...)` line for a `semantic_search` result, add:

```go
// Automatic Graph RAG: expand search results to subgraph context for richer LLM context.
if r.fcName == "semantic_search" && r.result.Success {
    if graphCtx := ExpandSearchResultsToSubgraph(ctx, app, r.result.Result); graphCtx != "" {
        // Inject as an additional function response part so the LLM receives it in the same turn.
        functionResponses = append(functionResponses, &genai.Part{
            FunctionResponse: &genai.FunctionResponse{
                Name:     "graph_expand",
                Response: map[string]any{"result": graphCtx},
            },
        })
        infra.LoggerFrom(ctx).Debug("graph_rag injected", "query_run_id", queryRunID, "nodes_in_context", strings.Count(graphCtx, "\n"))
    }
}
```

Note: injecting as a `FunctionResponse` named `"graph_expand"` is safe — the LLM treats it as supplemental context. The name does not need to correspond to an actual pending function call for Gemini to process it.

Also add `"graph_expand"` to the `searchTools` map (found in two places in `foh.go`, lines ~207 and ~438) so it participates in the repeat-backoff logic:

```go
searchTools := map[string]bool{
    "semantic_search": true, "get_entity_network": true, "graph_expand": true,
    // ... rest unchanged
}
```

- [ ] **Step 3: Build and run all agent tests**

```bash
go build ./...
go test ./internal/agent/... -v
```

Expected: no new failures.

- [ ] **Step 4: Run all tests**

```bash
go test ./... 2>&1 | tail -30
```

- [ ] **Step 5: Commit**

```bash
git add internal/agent/foh.go
git commit -m "feat(agent): wire Graph RAG auto-expansion into FOH after semantic_search"
```

---

## Task 6: Update docs

**Files:**
- Modify: `internal/prompts/app_capabilities.txt`
- Modify: `blueprint.md`

Per project rules: `app_capabilities.txt` must be updated for any capability change. `blueprint.md` must be checked and updated for core agentic loop changes. This plan modifies `foh.go`.

- [ ] **Step 1: Update `internal/prompts/app_capabilities.txt`**

Under the tools section, add:
```
- graph_expand(node_uuid, depth=1): Expand a knowledge node 1-2 hops via BFS traversal; returns connected entities, facts, and relationships. UUID lines appear in semantic_search results.
```

Under the FOH/agent section, add:
```
- Graph RAG: after semantic_search, the FOH automatically traverses 1-hop subgraphs from the top 3 result nodes and injects the combined subgraph block into the LLM context for richer entity understanding.
```

- [ ] **Step 2: Read and update `blueprint.md`**

Find the FOH description section. Add a note about the Graph RAG step in the query pipeline:
```
After semantic_search completes, the FOH auto-expands results via graph traversal (graph_rag.go) and injects a subgraph context block into the next LLM turn.
```

- [ ] **Step 3: Commit**

```bash
git add internal/prompts/app_capabilities.txt blueprint.md
git commit -m "docs: update app_capabilities and blueprint with graph_expand and Graph RAG"
```

---

## Smoke Test (Manual)

```bash
# Start local server
./scripts/test-local.sh

# Query something person-related — should trigger semantic_search + auto Graph RAG
curl -X POST http://localhost:8080/query \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $JOT_API_KEY" \
  -d '{"question": "Tell me about Gloria"}'

# In logs, confirm:
# - "graph_rag expanded context" with total_nodes > 1
# - The LLM response should reference connected facts not directly in the top search result
```

---

## Done Criteria

- [ ] `pkg/memory/graph.go` compiles and all 5 tests in `pkg/memory/graph_test.go` pass
- [ ] `formatKnowledgeNodes` emits `UUID: <id>` lines; test verifies it
- [ ] `graph_expand` tool registered via `registerGraphTools()` called from existing `init()`; appears in `TestRegisteredTools`
- [ ] All 4 `TestExtractUUIDs*` tests pass
- [ ] `ExpandSearchResultsToSubgraph` called after `semantic_search` in FOH
- [ ] `graph_expand` added to `searchTools` map in `foh.go`
- [ ] `go build ./...` is clean
- [ ] `app_capabilities.txt` and `blueprint.md` updated
