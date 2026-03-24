# Project Loom — Phases 3 & 4: Dynamic Refinery, Hot-Edge Eviction & Full Workers

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hardcoded predicate map with a Firestore-backed `CanonicalMapConfig`, wire the refinery to auto-extend it via `NEW:` predicates, implement hot-edge eviction, and fill in the Task and Response Worker stubs with real 2-hop RAG logic.

**Architecture:** `refineryResolveCommit` fetches the live canonical map from `_config/canonical_map` at runtime and appends newly discovered predicates back to it. `updateHotEdges` maintains a bounded 20-slot hot-edges array on object nodes with relevance-score eviction. `runTaskWorker` is fully ported from the evaluator. `runResponseWorker` performs 2-hop context retrieval (vector search → hot_edges expansion → pending tasks) and stores a `NodeTypeResponse` node. Dead code from the old pipeline is removed after worker logic is proved out.

**Tech Stack:** Go 1.22+, Firestore SDK, `internal/infra`, `memory` package, `internal/agent`

**Prerequisites:** Plan `2026-03-23-project-loom-phases-1-2.md` complete.

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `internal/agent/refinery_pipeline.go` | Dynamic canonical map, NEW: predicate handling, updateHotEdges |
| Modify | `internal/agent/loom_workers.go` | Full runTaskWorker (no change from stub), full runResponseWorker |
| Create | `internal/agent/loom_rag.go` | 2-hop RAG context builder used by runResponseWorker |
| Delete | `internal/agent/evaluator.go` (partial) | Remove `RunEvaluator` (keeps `RunEvaluatorExtract` used by task worker) |
| Modify | `internal/agent/process_entry.go` | Remove `AnalyzeJournalEntry`+`ResolveAndLinkEntities` calls from `ProcessEntry` (or add deprecation comment) |

---

## Task 1: Fetch `CanonicalMapConfig` from Firestore

**Files:**
- Modify: `internal/agent/refinery_pipeline.go`

The canonical map is stored at `_config/canonical_map`. On cold start (doc not found), fall back to `memory.AllowedPredicates`.

- [ ] **Step 1: Add Firestore constants and fetch helper**

At the top of `refinery_pipeline.go` (after imports), add:

```go
const (
	canonicalMapCollection = "_config"
	canonicalMapDocID      = "canonical_map"
)

// fetchCanonicalMap retrieves the live CanonicalMapConfig from Firestore.
// Returns a populated config using memory.AllowedPredicates as fallback if the
// document doesn't exist or the fetch fails.
func fetchCanonicalMap(ctx context.Context, app *infra.App) (memory.CanonicalMapConfig, error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return fallbackCanonicalMap(), fmt.Errorf("fetchCanonicalMap: firestore client: %w", err)
	}
	doc, err := client.Collection(canonicalMapCollection).Doc(canonicalMapDocID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			infra.LoggerFrom(ctx).Info("canonical map doc not found, using defaults")
			return fallbackCanonicalMap(), nil
		}
		return fallbackCanonicalMap(), fmt.Errorf("fetchCanonicalMap: get doc: %w", err)
	}
	data := doc.Data()
	cfg := memory.CanonicalMapConfig{}
	if v, ok := data["allowed_predicates"].([]any); ok {
		cfg.AllowedPredicates = make([]string, 0, len(v))
		for _, p := range v {
			if s, ok := p.(string); ok && s != "" {
				cfg.AllowedPredicates = append(cfg.AllowedPredicates, s)
			}
		}
	}
	if v, ok := data["entity_types"].([]any); ok {
		cfg.EntityTypes = make([]string, 0, len(v))
		for _, t := range v {
			if s, ok := t.(string); ok && s != "" {
				cfg.EntityTypes = append(cfg.EntityTypes, s)
			}
		}
	}
	if len(cfg.AllowedPredicates) == 0 {
		cfg.AllowedPredicates = memory.AllowedPredicates
	}
	return cfg, nil
}

func fallbackCanonicalMap() memory.CanonicalMapConfig {
	return memory.CanonicalMapConfig{
		AllowedPredicates: memory.AllowedPredicates,
		EntityTypes: []string{
			memory.NodeTypePerson, memory.NodeTypePlace, memory.NodeTypeProject,
			memory.NodeTypeEvent, memory.NodeTypeTool, memory.NodeTypeAsset,
			memory.NodeTypeObject,
		},
	}
}
```

Add required imports if not present: `google.golang.org/grpc/codes`, `google.golang.org/grpc/status`.

- [ ] **Step 2: Verify compile**

```bash
cd ../jot-project-loom && go build ./internal/agent/...
```

- [ ] **Step 3: Commit**

```bash
cd ../jot-project-loom
git add internal/agent/refinery_pipeline.go
git commit -m "feat(loom/refinery): add fetchCanonicalMap with AllowedPredicates fallback"
```

---

## Task 2: Update `refineryExtract` to Use Dynamic Canonical Map

**Files:**
- Modify: `internal/agent/refinery_pipeline.go`

Replace the `memory.AllowedPredicates` static join in `refineryExtract` with the live fetched map. Also update the prompt instructions to recognize `NEW:predicate_name` output.

- [ ] **Step 1: Change `runRefineryPipeline` to fetch and pass the map**

```go
func runRefineryPipeline(ctx context.Context, app *infra.App, entryUUID, content string) error {
	ctx, span := infra.StartSpan(ctx, "agent.refinery_pipeline")
	defer span.End()
	span.SetAttributes(map[string]string{"entry_uuid": entryUUID})

	canonMap, err := fetchCanonicalMap(ctx, app)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("refinery: canonical map fetch failed, using fallback", "error", err)
	}

	// NOTE: Discovery (12-node vector search for prior context) is REMOVED per Loom spec.
	// Context retrieval is now the responsibility of Stage 4 (Response Worker) only.

	triples, err := refineryExtract(ctx, app, entryUUID, content, canonMap)
	if err != nil {
		return fmt.Errorf("refinery extract: %w", err)
	}
	if len(triples) == 0 {
		infra.LoggerFrom(ctx).Debug("refinery: no triples", "entry_uuid", entryUUID)
		return nil
	}
	return refineryResolveCommit(ctx, app, entryUUID, triples, canonMap)
}
```

- [ ] **Step 2: Update `refineryExtract` signature and prompt**

```go
func refineryExtract(ctx context.Context, app *infra.App, entryUUID, content string, canonMap memory.CanonicalMapConfig) ([]refineryTriple, error) {
	ctx, span := infra.StartSpan(ctx, "agent.refinery_extract")
	defer span.End()

	predicateList := strings.Join(canonMap.AllowedPredicates, ", ")
	prompt, err := prompts.BuildRefinery(prompts.RefineryData{
		Entry:             utils.WrapAsUserData(utils.SanitizePrompt(content)),
		AllowedPredicates: utils.WrapAsUserData(predicateList),
		// Discovery context removed; the prompt template must be updated to remove that field.
	})
	if err != nil {
		return nil, fmt.Errorf("build refinery prompt: %w", err)
	}
	raw, err := infra.GenerateContentSimple(ctx, app, "", prompt+prompts.DataSafety(), app.Config(), &infra.GenConfig{MaxOutputTokens: 300})
	if err != nil {
		return nil, fmt.Errorf("refinery llm call: %w", err)
	}
	infra.LoggerFrom(ctx).Debug("refinery raw output", "entry_uuid", entryUUID, "output", raw)
	simple, sections := utils.ParseKeyValueMap(raw)
	if strings.EqualFold(simple["status"], "none") {
		return nil, nil
	}
	lines := sections["triples"]
	return parseRefineryTriples(lines), nil
}
```

**Note:** Update `prompts.RefineryData` struct to remove the `Discovery` field and add instructions about `NEW:` predicates. Find it in `internal/prompts/` and update both the struct and the template to:
- Remove `Discovery` field
- Add instruction: "If you identify a high-value predicate not in the list, output it as `NEW:predicate_name` in the predicate field."

- [ ] **Step 3: Remove `refineryDiscovery` function**

Delete the entire `refineryDiscovery` function from `refinery_pipeline.go` — it's now dead code (replaced by Stage 4 context retrieval).

- [ ] **Step 4: Verify compile**

```bash
cd ../jot-project-loom && go build ./internal/agent/... && go build ./...
```

- [ ] **Step 5: Commit**

```bash
cd ../jot-project-loom
git add internal/agent/refinery_pipeline.go
git commit -m "feat(loom/refinery): use dynamic canonical map; remove pre-refinery discovery"
```

---

## Task 3: Auto-Update Canonical Map on `NEW:` Predicates

**Files:**
- Modify: `internal/agent/refinery_pipeline.go`

In `refineryResolveCommit`, detect `NEW:` prefix, strip it, append to Firestore, and proceed.

- [ ] **Step 1: Update `refineryResolveCommit` signature to accept `canonMap`**

```go
func refineryResolveCommit(ctx context.Context, app *infra.App, entryUUID string, triples []refineryTriple, canonMap memory.CanonicalMapConfig) error {
```

- [ ] **Step 2: Add `NEW:` detection and append logic**

Inside the triple loop, before the existing `CanonicalizePredicate` call:

```go
rawPred := t.Predicate
if strings.HasPrefix(strings.ToUpper(rawPred), "NEW:") {
    newPred := memory.NormalizedPredicate(strings.TrimPrefix(strings.TrimPrefix(rawPred, "NEW:"), "new:"))
    if newPred != "" {
        if appendErr := appendPredicateToCanonicalMap(ctx, app, newPred); appendErr != nil {
            infra.LoggerFrom(ctx).Warn("refinery: failed to append new predicate to canonical map",
                "predicate", newPred, "error", appendErr)
        } else {
            infra.LoggerFrom(ctx).Info("refinery: new predicate appended to canonical map", "predicate", newPred)
        }
        t.Predicate = newPred
    }
}
```

- [ ] **Step 3: Add `appendPredicateToCanonicalMap` helper**

```go
func appendPredicateToCanonicalMap(ctx context.Context, app *infra.App, predicate string) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("appendPredicateToCanonicalMap: firestore client: %w", err)
	}
	ref := client.Collection(canonicalMapCollection).Doc(canonicalMapDocID)
	_, err = ref.Update(ctx, []firestore.Update{
		{Path: "allowed_predicates", Value: firestore.ArrayUnion(predicate)},
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			// Document doesn't exist yet; create it with the new predicate.
			initial := map[string]any{
				"allowed_predicates": append(memory.AllowedPredicates, predicate),
				"entity_types":       fallbackCanonicalMap().EntityTypes,
			}
			_, err = ref.Set(ctx, initial)
			return err
		}
		return fmt.Errorf("appendPredicateToCanonicalMap: update: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Verify compile**

```bash
cd ../jot-project-loom && go build ./internal/agent/...
```

- [ ] **Step 5: Commit**

```bash
cd ../jot-project-loom
git add internal/agent/refinery_pipeline.go
git commit -m "feat(loom/refinery): auto-append NEW: predicates to canonical_map singleton"
```

---

## Task 4: Implement `updateHotEdges`

**Files:**
- Modify: `internal/agent/refinery_pipeline.go`

- [ ] **Step 1: Add `updateHotEdges` function**

```go
const maxHotEdges = 20

// updateHotEdges maintains a bounded 20-slot hot-edges array on objectNodeID.
// The new relationship node starts with relevance_score = 1.0.
// If the array is full, the existing relationship with the lowest relevance_score is evicted.
func updateHotEdges(ctx context.Context, app *infra.App, objectNodeID, newRelationshipID string) error {
	ctx, span := infra.StartSpan(ctx, "loom.update_hot_edges")
	defer span.End()
	span.SetAttributes(map[string]string{"object_node_id": objectNodeID, "new_relationship_id": newRelationshipID})

	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("updateHotEdges: firestore client: %w", err)
	}
	col := client.Collection(memory.KnowledgeCollection)

	// Set the new relationship's relevance_score to 1.0.
	if _, err := col.Doc(newRelationshipID).Update(ctx, []firestore.Update{
		{Path: "relevance_score", Value: 1.0},
	}); err != nil {
		infra.LoggerFrom(ctx).Warn("updateHotEdges: failed to set new rel score", "rel_id", newRelationshipID, "error", err)
	}

	// Fetch the object node's current hot_edges.
	objDoc, err := col.Doc(objectNodeID).Get(ctx)
	if err != nil {
		return fmt.Errorf("updateHotEdges: fetch object node: %w", err)
	}
	data := objDoc.Data()
	var hotEdges []string
	if v, ok := data["hot_edges"].([]any); ok {
		hotEdges = make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				hotEdges = append(hotEdges, s)
			}
		}
	}

	if len(hotEdges) < maxHotEdges {
		// Slot available — just append.
		hotEdges = append(hotEdges, newRelationshipID)
		_, err = col.Doc(objectNodeID).Update(ctx, []firestore.Update{
			{Path: "hot_edges", Value: hotEdges},
		})
		return err
	}

	// Array full — fetch relevance_scores of all existing edges and evict the lowest.
	type edgeScore struct {
		id    string
		score float64
	}
	scores := make([]edgeScore, 0, len(hotEdges))
	for _, edgeID := range hotEdges {
		edgeDoc, err := col.Doc(edgeID).Get(ctx)
		if err != nil {
			// If we can't fetch a score, treat it as 0 (safe to evict).
			scores = append(scores, edgeScore{id: edgeID, score: 0})
			infra.LoggerFrom(ctx).Warn("updateHotEdges: fetch edge score failed, treating as 0", "edge_id", edgeID, "error", err)
			continue
		}
		var score float64
		if v, ok := edgeDoc.Data()["relevance_score"].(float64); ok {
			score = v
		}
		scores = append(scores, edgeScore{id: edgeID, score: score})
	}

	// Find lowest-scored edge.
	lowestIdx := 0
	for i, s := range scores {
		if s.score < scores[lowestIdx].score {
			lowestIdx = i
		}
	}
	infra.LoggerFrom(ctx).Info("updateHotEdges: evicting low-score edge",
		"object_node_id", objectNodeID,
		"evicted_edge_id", scores[lowestIdx].id,
		"evicted_score", scores[lowestIdx].score,
	)
	hotEdges[lowestIdx] = newRelationshipID
	_, err = col.Doc(objectNodeID).Update(ctx, []firestore.Update{
		{Path: "hot_edges", Value: hotEdges},
	})
	return err
}
```

- [ ] **Step 2: Wire `updateHotEdges` into `refineryResolveCommit`**

After the existing `app.Memory.AddEntityLink(ctx, obj.UUID, relID)` call, add:

```go
// Update hot-edges on the object node for graph cache maintenance.
if heErr := updateHotEdges(ctx, app, obj.UUID, relID); heErr != nil {
    infra.LoggerFrom(ctx).Warn("refinery: updateHotEdges failed (non-fatal)", "object_uuid", obj.UUID, "rel_id", relID, "error", heErr)
}
```

- [ ] **Step 3: Verify compile + tests**

```bash
cd ../jot-project-loom && go build ./internal/agent/... && go test ./internal/agent/...
```

- [ ] **Step 4: Commit**

```bash
cd ../jot-project-loom
git add internal/agent/refinery_pipeline.go
git commit -m "feat(loom/refinery): implement updateHotEdges with score-based eviction"
```

---

## Task 5: Create `loom_rag.go` — 2-Hop Context Builder

**Files:**
- Create: `internal/agent/loom_rag.go`

This module is used by `runResponseWorker` for context retrieval.

- [ ] **Step 1: Write the file**

```go
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/hoyle1974/memory"
)

// LoomRAGContext holds the assembled 2-hop context for the response worker.
type LoomRAGContext struct {
	RelationshipSummaries []string // top-5 relationships similar to the log
	HopNodeSummaries      []string // subject/object nodes expanded from hot_edges
	PendingTaskSummaries  []string // pending tasks for the user
}

// BuildLoomRAGContext performs 2-hop context retrieval for a log entry:
//  1. Vector-search top-5 relationship nodes similar to logContent.
//  2. For each, fetch their subject/object nodes and expand their hot_edges.
//  3. Fetch pending tasks.
func BuildLoomRAGContext(ctx context.Context, app *infra.App, logContent string) (*LoomRAGContext, error) {
	ctx, span := infra.StartSpan(ctx, "loom.build_rag_context")
	defer span.End()

	result := &LoomRAGContext{}

	// Step 1: Vector search for top-5 relationship nodes.
	queryVec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, logContent, infra.EmbedTaskRetrievalQuery)
	if err != nil {
		return result, fmt.Errorf("loom rag: embedding: %w", err)
	}
	similarNodes, err := app.Memory.QuerySimilarNodes(ctx, queryVec, 5)
	if err != nil {
		return result, fmt.Errorf("loom rag: query similar: %w", err)
	}

	// Step 2: For each relationship, expand subject/object + their hot_edges.
	seenIDs := make(map[string]bool)
	client, fsErr := app.Firestore(ctx)
	if fsErr != nil {
		return result, fmt.Errorf("loom rag: firestore: %w", fsErr)
	}
	col := client.Collection(memory.KnowledgeCollection)

	for _, rel := range similarNodes {
		result.RelationshipSummaries = append(result.RelationshipSummaries,
			fmt.Sprintf("[rel] %s | %s | subj=%s obj=%s", rel.UUID, rel.Content, rel.SubjectUUID, rel.ObjectUUID))

		for _, nodeID := range []string{rel.SubjectUUID, rel.ObjectUUID} {
			if nodeID == "" || seenIDs[nodeID] {
				continue
			}
			seenIDs[nodeID] = true
			nodeDoc, err := col.Doc(nodeID).Get(ctx)
			if err != nil {
				infra.LoggerFrom(ctx).Warn("loom rag: fetch node failed", "node_id", nodeID, "error", err)
				continue
			}
			nodeData := nodeDoc.Data()
			nodeSummary := fmt.Sprintf("[node] %s | %s", nodeID, getStringFieldFromMap(nodeData, "content"))
			result.HopNodeSummaries = append(result.HopNodeSummaries, nodeSummary)

			// Expand hot_edges of this node (1 more hop).
			if hotEdges, ok := nodeData["hot_edges"].([]any); ok {
				for _, he := range hotEdges {
					heID, _ := he.(string)
					if heID == "" || seenIDs[heID] {
						continue
					}
					seenIDs[heID] = true
					heDoc, err := col.Doc(heID).Get(ctx)
					if err != nil {
						continue
					}
					heData := heDoc.Data()
					result.HopNodeSummaries = append(result.HopNodeSummaries,
						fmt.Sprintf("[hot-edge] %s | %s", heID, getStringFieldFromMap(heData, "content")))
				}
			}
		}
	}

	// Step 3: Fetch pending tasks.
	tasks, err := app.Memory.GetTasksByStatus(ctx, memory.TaskStatusPending, 10)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("loom rag: fetch pending tasks failed", "error", err)
	}
	for _, t := range tasks {
		result.PendingTaskSummaries = append(result.PendingTaskSummaries,
			fmt.Sprintf("[task] %s | %s", t.UUID, t.Content))
	}
	return result, nil
}

// FormatForPrompt returns a prompt-ready string block from the RAG context.
func (r *LoomRAGContext) FormatForPrompt() string {
	var b strings.Builder
	if len(r.RelationshipSummaries) > 0 {
		b.WriteString("## Related Graph Edges\n")
		for _, s := range r.RelationshipSummaries {
			b.WriteString("- " + s + "\n")
		}
	}
	if len(r.HopNodeSummaries) > 0 {
		b.WriteString("\n## Expanded Context Nodes\n")
		for _, s := range r.HopNodeSummaries {
			b.WriteString("- " + s + "\n")
		}
	}
	if len(r.PendingTaskSummaries) > 0 {
		b.WriteString("\n## Pending Tasks\n")
		for _, s := range r.PendingTaskSummaries {
			b.WriteString("- " + s + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func getStringFieldFromMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
```

**Note:** `app.Memory.GetTasksByStatus` may not exist yet. Check `memory/task_query.go`. If missing, use `app.Memory.ListTasks(ctx)` and filter in-process. Adapt as needed.

- [ ] **Step 2: Verify compile**

```bash
cd ../jot-project-loom && go build ./internal/agent/...
```

- [ ] **Step 3: Commit**

```bash
cd ../jot-project-loom
git add internal/agent/loom_rag.go
git commit -m "feat(loom): add BuildLoomRAGContext 2-hop retrieval"
```

---

## Task 6: Fill In `runResponseWorker`

**Files:**
- Modify: `internal/agent/loom_workers.go`

Replace the stub with real logic: build context, call LLM, store a `NodeTypeResponse` node.

- [ ] **Step 1: Update `runResponseWorker`**

```go
func runResponseWorker(ctx context.Context, app *infra.App, logUUID, logContent string, graphExtractFailed bool) error {
	ctx, span := infra.StartSpan(ctx, "loom.response_worker")
	defer span.End()

	ragCtx, err := BuildLoomRAGContext(ctx, app, logContent)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("loom response worker: RAG context failed", "log_uuid", logUUID, "error", err)
		// Continue with empty context.
	}

	contextBlock := ""
	if ragCtx != nil {
		contextBlock = ragCtx.FormatForPrompt()
	}
	graphNote := ""
	if graphExtractFailed {
		graphNote = "\nNote: Knowledge graph extraction failed for this entry. Context may be incomplete.\n"
	}

	systemPrompt := "You are a personal memory assistant. Given the user's log entry and the related graph context, generate a concise, insightful response or observation. Then output a `logic_trace` paragraph explaining your reasoning step by step." + prompts.DataSafety()
	userPrompt := fmt.Sprintf("%s\n## Log Entry\n%s\n\n## Graph Context\n%s",
		graphNote,
		utils.WrapAsUserData(utils.SanitizePrompt(logContent)),
		utils.WrapAsUserData(contextBlock),
	)

	raw, err := infra.GenerateContentSimple(ctx, app, systemPrompt, userPrompt, app.Config(), &infra.GenConfig{MaxOutputTokens: 512})
	if err != nil {
		return fmt.Errorf("loom response worker: LLM call: %w", err)
	}

	simple, _ := utils.ParseKeyValueMap(raw)
	responseText := strings.TrimSpace(simple["response"])
	logicTrace := strings.TrimSpace(simple["logic_trace"])
	if responseText == "" {
		responseText = strings.TrimSpace(raw) // fallback: use full output if KV parsing found nothing
	}

	// Store as a NodeTypeResponse node linked to the source log.
	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("loom response worker: firestore: %w", err)
	}
	respDoc := map[string]any{
		"content":         responseText,
		"node_type":       memory.NodeTypeResponse,
		"source_entry_id": logUUID,
		"logic_trace":     logicTrace,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	}
	respUUID := generateLoomUUID()
	if _, setErr := client.Collection(memory.KnowledgeCollection).Doc(respUUID).Set(ctx, respDoc); setErr != nil {
		return fmt.Errorf("loom response worker: write response node: %w", setErr)
	}
	infra.LoggerFrom(ctx).Info("loom response worker: response node stored",
		"log_uuid", logUUID,
		"response_uuid", respUUID,
		"logic_trace_len", len(logicTrace),
	)
	return nil
}
```

Add `generateLoomUUID()` helper at the bottom of `loom_workers.go`:

```go
func generateLoomUUID() string {
	return fmt.Sprintf("resp-%s", utils.NewUUID()) // or use uuid.New().String() directly
}
```

Check what UUID generation util already exists (`pkg/utils` or `github.com/google/uuid`). Adapt.

Also add required imports to `loom_workers.go`: `"fmt"`, `"time"`, `"github.com/jackstrohm/jot/internal/prompts"`.

- [ ] **Step 2: Verify compile**

```bash
cd ../jot-project-loom && go build ./internal/agent/...
```

- [ ] **Step 3: Commit**

```bash
cd ../jot-project-loom
git add internal/agent/loom_workers.go
git commit -m "feat(loom): implement runResponseWorker with 2-hop RAG and response node storage"
```

---

## Task 7: Dead Code Cleanup

These components are made obsolete by the Loom pipeline. Remove them after the pipeline is verified working.

### 7a: Remove `refineryDiscovery` (already done in Task 2, confirm)

- [ ] Confirm `refineryDiscovery` function is deleted from `refinery_pipeline.go`.

### 7b: Remove `RunEvaluator` from `evaluator.go`

`RunEvaluator` (the wrapper that also fires `runProactiveInsight`) is replaced by `runTaskWorker` calling `RunEvaluatorExtract` directly. `RunEvaluatorExtract` is still needed.

- [ ] Delete `RunEvaluator` function from `internal/agent/evaluator.go` (~lines 97-122).
- [ ] Delete `runProactiveInsight` function from `internal/agent/evaluator.go` (~lines 126-148).
- [ ] Remove `ProactiveAlertSignificanceThreshold` constant if no other callers remain.
- [ ] Verify: `grep -r "RunEvaluator\b" internal/` — should return zero results.

### 7c: Remove `AnalyzeJournalEntry` + `ResolveAndLinkEntities` from `ProcessEntry`

These are replaced by Loom Stage 2 (Refinery handles entity resolution natively).

- [ ] In `internal/agent/process_entry.go`, remove the `AnalyzeJournalEntry` call and `ResolveAndLinkEntities` call from `ProcessEntry` (lines ~85-102).
- [ ] Remove `analysisJSON` variable and the `journal_analysis` update from the Firestore update batch.
- [ ] Add a comment: `// TODO(loom): ProcessEntry is the legacy path. Prefer ProcessLogSequential.`

### 7d: Remove `updateEntryWithRetry`

`ProcessLogSequential` Stage 1 guarantees the doc exists, making the retry backoff unnecessary in the new path. The legacy `ProcessEntry` still uses it — leave it in place with a deprecation comment for now. Remove once `ProcessEntry` is fully replaced.

- [ ] Add comment above `updateEntryWithRetry`: `// Deprecated: used only by ProcessEntry legacy path. Remove after migration to ProcessLogSequential.`

- [ ] **Step: Full build and test after cleanup**

```bash
cd ../jot-project-loom && go build ./... && go test ./...
```

Expected: no regressions.

- [ ] **Step: Commit cleanup**

```bash
cd ../jot-project-loom
git add internal/agent/evaluator.go internal/agent/process_entry.go internal/agent/refinery_pipeline.go
git commit -m "refactor(loom): remove dead code — RunEvaluator, AnalyzeJournalEntry, refineryDiscovery"
```

---

## Task 8: Final Verification

- [ ] `go build ./...` — clean
- [ ] `go test ./...` — no regressions
- [ ] `go vet ./...` — clean
- [ ] Manual smoke: trigger a `ProcessLogSequential` call via admin CLI or test and confirm all 4 stage log lines appear in Cloud Logging.
