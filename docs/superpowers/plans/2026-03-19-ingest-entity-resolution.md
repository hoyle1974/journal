# Ingest-Time Entity Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `ProcessEntry` to resolve entity mentions in new journal entries against existing knowledge nodes at write time — so "Gloria" in a new note is immediately linked to her person node, and "Gloria is my wife" creates or strengthens the SPO relationship, without waiting for the nightly Dreamer.

**Architecture:** Replace `LinkEntryToPeople` (persons only, background goroutine) with two new functions in `internal/agent/graph_builder.go`: `ResolveAndLinkEntities` (all entity types, synchronous with internal timeout) and `ExtractAndStoreRelationships` (LLM call for SPO triples, background goroutine). Both are called from `ProcessEntry`. A new `relationship_extractor.txt` prompt template handles the SPO extraction prompt. The old `LinkEntryToPeople` is removed after `process_entry.go` is updated.

**Tech Stack:** Go, existing `pkg/memory` (FindEntityNodeByName, UpsertKnowledge, AddEntityLink, AppendJournalEntryIDsToNode, MetadataToJSON), `journal.AnalyzeJournalEntry` output, `infra.GenerateContentSimple`, `text/template`, `//go:embed`

---

## Context: What Already Exists

Read these files before touching anything:

- `internal/agent/process_entry.go:112` — calls `go LinkEntryToPeople(bgCtx, app, entryUUID, analysis.Entities)` (persons only, background goroutine, never creates nodes)
- `internal/agent/graph_builder.go:13` — `LinkEntryToPeople`: loops over entities, skips non-persons, calls `memory.FindEntityNodeByName` + `memory.AppendJournalEntryIDsToNode`. This is the only caller.
- `pkg/memory/knowledge.go:665` — `FindEntityNodeByName`: vector search by name, returns `*KnowledgeNode`
- `pkg/memory/knowledge.go:54` — `UpsertKnowledge`: creates/updates a knowledge node with embedding
- `pkg/memory/knowledge.go:360` — `AddEntityLink`: idempotent link from source UUID to target UUID
- `pkg/memory/schema.go:452` — `ParseSPOTriple`: parses "Subject | Predicate | Object" lines
- `pkg/memory/schema.go:476` — `NormalizedPredicate`: lowercases and snake_cases a predicate string
- `pkg/memory/schema.go` — `MetadataToJSON`, `NodeTypeGeneric`
- `internal/prompts/prompts.go` — **exact embed pattern**: each file gets its own `//go:embed <filename>` var (e.g., `var tagConsolidatorTxt string`), then a package-level var using `template.Must(template.New("name").Parse(theVar))`. Use this pattern exactly.
- `internal/infra/gemini.go` — `GenerateContentSimple(ctx, env infra.ToolEnv, systemPrompt, userPrompt string, cfg *config.Config, genCfg *infra.GenConfig) (string, error)`. Note: `*infra.App` implements `infra.ToolEnv` — passing `app` directly is safe.

The gap:
1. Only persons are linked; places, orgs, and other entity types are ignored
2. No SPO relationship nodes are created at ingest time
3. `LinkEntryToPeople` never creates a new entity node if the person doesn't exist yet (skip on miss)

---

## File Map

| Action | File | Responsibility |
|---|---|---|
| **Create** | `internal/prompts/relationship_extractor.txt` | LLM prompt for SPO triple extraction |
| **Modify** | `internal/prompts/prompts.go` | Add embed var + template var + `RelationshipExtractorData` + `BuildRelationshipExtractor` |
| **Modify** | `internal/agent/graph_builder.go` | Replace `LinkEntryToPeople` with `ResolveAndLinkEntities` + `ExtractAndStoreRelationships` |
| **Create** | `internal/agent/graph_builder_test.go` | Tests for `parseSPOLines` |
| **Modify** | `internal/agent/process_entry.go` | Wire new functions; remove old goroutine |
| **Modify** | `internal/prompts/app_capabilities.txt` | Document new ingest behavior |

---

## Task 1: `relationship_extractor.txt` prompt + prompts.go additions

**Files:**
- Create: `internal/prompts/relationship_extractor.txt`
- Modify: `internal/prompts/prompts.go`

- [ ] **Step 1: Write the failing test**

If `internal/prompts/prompts_test.go` does not exist, create it. Add:

```go
package prompts_test

import (
	"strings"
	"testing"

	"github.com/jackstrohm/jot/internal/prompts"
)

func TestBuildRelationshipExtractor_ContainsContent(t *testing.T) {
	data := prompts.RelationshipExtractorData{
		Content: "Gloria is my wife and she loves hiking.",
	}
	out, err := prompts.BuildRelationshipExtractor(data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(out, "Gloria is my wife") {
		t.Errorf("expected content in rendered prompt, got:\n%s", out)
	}
	if !strings.Contains(out, "Subject | Predicate | Object") {
		t.Errorf("expected SPO format instructions in prompt, got:\n%s", out)
	}
}

func TestBuildRelationshipExtractor_EmptyContent(t *testing.T) {
	// Empty content must render without error (not panic).
	data := prompts.RelationshipExtractorData{Content: ""}
	_, err := prompts.BuildRelationshipExtractor(data)
	if err != nil {
		t.Fatalf("expected no error for empty content, got: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/jstrohm/code/jot
go test ./internal/prompts/... -run TestBuildRelationshipExtractor -v
```

Expected: compile error — `prompts.RelationshipExtractorData` and `prompts.BuildRelationshipExtractor` not defined.

- [ ] **Step 3: Create `internal/prompts/relationship_extractor.txt`**

```
You are an expert at extracting structured relationships from personal journal entries.

Given the journal entry below, extract explicit relationship facts as Subject | Predicate | Object triples.

Rules:
- Only extract facts that are clearly stated, not inferred
- Subject and Object should be named entities (people, places, organizations, projects)
- Predicate should be a short verb phrase normalized to snake_case (e.g. "works at" → "works_at", "is child of" → "is_child_of")
- One triple per line, no numbering, no preamble, no explanation
- If no clear relationships exist in the entry, output exactly: NONE

Output format (one per line):
Subject | predicate | Object

Positive example:
Entry: "Gloria is my wife. She works at Anthropic. Gideon is our son."
Output:
Gloria | is_wife_of | Jeff
Gloria | works_at | Anthropic
Gideon | is_child_of | Gloria

Negative example (no relationships — output NONE):
Entry: "Today was a quiet day. I read a book and took a walk."
Output: NONE

Entry:
<user_data>
{{ .Content }}
</user_data>
```

- [ ] **Step 4: Add to `internal/prompts/prompts.go`**

Follow the exact pattern from the existing file. Add in three places:

**A. Add embed var** (with the other `//go:embed` lines):
```go
//go:embed relationship_extractor.txt
var relationshipExtractorTxt string
```

**B. Add template var** (with the other `template.Must(...)` lines):
```go
relationshipExtractorTmpl = template.Must(template.New("relationshipExtractor").Parse(relationshipExtractorTxt))
```

**C. Add data struct and builder function** (at the end of the file, following the existing pattern):
```go
// RelationshipExtractorData holds the entry content for SPO relationship extraction.
type RelationshipExtractorData struct {
	Content string
}

// BuildRelationshipExtractor executes the relationship-extractor template.
func BuildRelationshipExtractor(data RelationshipExtractorData) (string, error) {
	var buf bytes.Buffer
	if err := relationshipExtractorTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute relationship extractor: %w", err)
	}
	return buf.String(), nil
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/prompts/... -run TestBuildRelationshipExtractor -v
```

Expected: both tests PASS.

- [ ] **Step 6: Build check**

```bash
go build ./...
```

- [ ] **Step 7: Commit**

```bash
git add internal/prompts/relationship_extractor.txt internal/prompts/prompts.go internal/prompts/prompts_test.go
git commit -m "feat(prompts): add relationship_extractor prompt template for SPO extraction at ingest"
```

---

## Task 2: `parseSPOLines`, `ResolveAndLinkEntities`, `ExtractAndStoreRelationships`

**Files:**
- Modify: `internal/agent/graph_builder.go`
- Create: `internal/agent/graph_builder_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/agent/graph_builder_test.go
package agent

import (
	"testing"
)

func TestParseSPOLines_Valid(t *testing.T) {
	input := "Gloria | is_wife_of | Jeff\nGideon | is_child_of | Gloria\n"
	triples := parseSPOLines(input)
	if len(triples) != 2 {
		t.Fatalf("expected 2 triples, got %d", len(triples))
	}
	if triples[0].Subject != "Gloria" {
		t.Errorf("expected Subject=Gloria, got %q", triples[0].Subject)
	}
	if triples[0].Predicate != "is_wife_of" {
		t.Errorf("expected Predicate=is_wife_of, got %q", triples[0].Predicate)
	}
	if triples[0].Object != "Jeff" {
		t.Errorf("expected Object=Jeff, got %q", triples[0].Object)
	}
}

func TestParseSPOLines_None(t *testing.T) {
	triples := parseSPOLines("NONE")
	if len(triples) != 0 {
		t.Fatalf("expected 0 triples for NONE output, got %d", len(triples))
	}
}

func TestParseSPOLines_Empty(t *testing.T) {
	triples := parseSPOLines("")
	if len(triples) != 0 {
		t.Fatalf("expected 0 triples for empty input, got %d", len(triples))
	}
}

func TestParseSPOLines_Malformed(t *testing.T) {
	// Lines without exactly two "|" separators must be skipped.
	input := "Gloria is Jeff's wife\nGideon | is_child_of | Gloria\n"
	triples := parseSPOLines(input)
	if len(triples) != 1 {
		t.Fatalf("expected 1 valid triple (malformed line skipped), got %d", len(triples))
	}
	if triples[0].Subject != "Gideon" {
		t.Errorf("expected Subject=Gideon, got %q", triples[0].Subject)
	}
}

func TestParseSPOLines_PredicateNormalized(t *testing.T) {
	// Raw predicate "works at" should be normalized to "works_at".
	input := "Gloria | works at | Anthropic\n"
	triples := parseSPOLines(input)
	if len(triples) != 1 {
		t.Fatalf("expected 1 triple, got %d", len(triples))
	}
	if triples[0].Predicate != "works_at" {
		t.Errorf("expected normalized predicate works_at, got %q", triples[0].Predicate)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/agent/... -run TestParseSPOLines -v
```

Expected: compile error — `parseSPOLines` not defined.

- [ ] **Step 3: Replace `graph_builder.go` with the new implementation**

Replace the entire file (it only contains `LinkEntryToPeople` and is short):

```go
package agent

import (
	"context"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

// resolveTimeout caps the time spent on synchronous entity resolution per entry.
// Entity resolution performs vector searches per entity — budget 8 seconds total.
// Note: with multiple entities and ~500ms per vector search, ingest P99 latency may increase
// by 2-4 seconds. If observed in production, move ResolveAndLinkEntities to a goroutine.
const resolveTimeout = 8 * time.Second

// parseSPOLines parses LLM output lines into SPO triples.
// Skips "NONE", blank lines, and lines without exactly two "|" separators.
// Normalizes predicates to snake_case via memory.NormalizedPredicate.
func parseSPOLines(output string) []memory.SPOTriple {
	var result []memory.SPOTriple
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "NONE") {
			continue
		}
		triple := memory.ParseSPOTriple(line)
		if triple == nil {
			continue
		}
		triple.Predicate = memory.NormalizedPredicate(triple.Predicate)
		result = append(result, *triple)
	}
	return result
}

// ResolveAndLinkEntities resolves all entity mentions (persons, places, orgs, etc.) from journal
// analysis against existing knowledge nodes and appends the entry UUID to each matched node's
// journal_entry_ids. Runs synchronously within resolveTimeout. Failures are logged and swallowed —
// this is a best-effort enrichment step. If no node exists for an entity, it is skipped (node
// creation happens via the Dreamer or explicit upsert_knowledge tool calls).
func ResolveAndLinkEntities(ctx context.Context, app *infra.App, entryUUID string, entities []journal.Entity) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	ctx, span := infra.StartSpan(ctx, "agent.resolve_and_link_entities")
	defer span.End()

	for _, ent := range entities {
		if strings.TrimSpace(ent.Name) == "" {
			continue
		}
		node, err := memory.FindEntityNodeByName(ctx, app, ent.Name)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("resolve_entities find error", "entity", ent.Name, "error", err)
			continue
		}
		if node == nil {
			infra.LoggerFrom(ctx).Debug("resolve_entities no match", "entity", ent.Name, "type", ent.Type)
			continue
		}
		if err := memory.AppendJournalEntryIDsToNode(ctx, app, node.UUID, []string{entryUUID}); err != nil {
			infra.LoggerFrom(ctx).Debug("resolve_entities link failed", "entity", ent.Name, "node", node.UUID, "error", err)
			continue
		}
		infra.LoggerFrom(ctx).Debug("resolve_entities linked", "entity", ent.Name, "node_type", node.NodeType, "node_uuid", node.UUID, "entry", entryUUID)
	}
}

// ExtractAndStoreRelationships calls the LLM to extract SPO triples from the entry content,
// then upserts each triple as a generic knowledge node and links the subject and object nodes.
// Must be called in a goroutine — it makes an LLM call (~1-2s) that would unacceptably extend
// synchronous ingest latency. Failures are logged and swallowed; this must not block ingest.
func ExtractAndStoreRelationships(ctx context.Context, app *infra.App, entryUUID, content string) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	ctx, span := infra.StartSpan(ctx, "agent.extract_and_store_relationships")
	defer span.End()

	if len(strings.TrimSpace(content)) < 20 {
		return
	}

	prompt, err := prompts.BuildRelationshipExtractor(prompts.RelationshipExtractorData{Content: content})
	if err != nil {
		infra.LoggerFrom(ctx).Debug("relationship_extractor render failed", "error", err)
		return
	}

	// prompt contains both system instructions and the wrapped entry content.
	// Pass as systemPrompt; userPrompt is empty (matching the evaluator pattern in specialists.go).
	raw, err := infra.GenerateContentSimple(ctx, app, prompt, "", app.Config(), &infra.GenConfig{MaxOutputTokens: 256})
	if err != nil {
		infra.LoggerFrom(ctx).Debug("relationship_extractor llm failed", "error", err)
		return
	}
	infra.LoggerFrom(ctx).Debug("relationship_extractor raw output", "output", raw)

	triples := parseSPOLines(raw)
	if len(triples) == 0 {
		return
	}

	for _, triple := range triples {
		// e.g. "Gloria works_at Anthropic"
		nodeContent := triple.Subject + " " + triple.Predicate + " " + triple.Object

		subjNode, _ := memory.FindEntityNodeByName(ctx, app, triple.Subject)
		objNode, _ := memory.FindEntityNodeByName(ctx, app, triple.Object)

		entityLinks := []string{entryUUID}
		if subjNode != nil {
			entityLinks = append(entityLinks, subjNode.UUID)
		}
		if objNode != nil {
			entityLinks = append(entityLinks, objNode.UUID)
		}

		metaMap := map[string]any{
			// TruncateString is used here for Firestore metadata storage, not logging — the
			// no-truncation rule applies to Debug log values only.
			"source_excerpt": utils.TruncateString(content, 200),
			"extracted_facts": []string{
				triple.Subject + " | " + triple.Predicate + " | " + triple.Object,
			},
			"confidence_score": 0.8,
		}
		metaJSON, err := memory.MetadataToJSON(metaMap)
		if err != nil {
			metaJSON = "{}"
		}

		spoUUID, err := memory.UpsertKnowledge(ctx, app, nodeContent, memory.NodeTypeGeneric, metaJSON, entityLinks)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("relationship upsert failed", "triple", nodeContent, "error", err)
			continue
		}
		infra.LoggerFrom(ctx).Debug("relationship stored", "spo_uuid", spoUUID, "content", nodeContent)

		// Link subject node back to this SPO node for bidirectional traversal.
		if subjNode != nil {
			if err := memory.AddEntityLink(ctx, app, subjNode.UUID, spoUUID); err != nil {
				infra.LoggerFrom(ctx).Debug("spo subject backlink failed", "error", err)
			}
		}
	}

	infra.LoggerFrom(ctx).Debug("relationship_extractor done", "entry_uuid", entryUUID, "triples_stored", len(triples))
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/agent/... -run TestParseSPOLines -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Build check**

```bash
go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add internal/agent/graph_builder.go internal/agent/graph_builder_test.go
git commit -m "feat(agent): replace LinkEntryToPeople with ResolveAndLinkEntities (all types) and ExtractAndStoreRelationships (SPO)"
```

---

## Task 3: Wire into `process_entry.go` and remove dead code

**Files:**
- Modify: `internal/agent/process_entry.go`

- [ ] **Step 1: Locate the existing goroutine call**

In `process_entry.go`, find this block (line ~110-113):

```go
if analysis != nil && len(analysis.Entities) > 0 {
    bgCtx := context.Background()
    go LinkEntryToPeople(bgCtx, app, entryUUID, analysis.Entities)
}
```

- [ ] **Step 2: Replace it**

```go
if analysis != nil && len(analysis.Entities) > 0 {
    // Synchronous entity resolution with internal timeout. Resolves entity mentions to
    // existing knowledge nodes and links this entry to them.
    ResolveAndLinkEntities(ctx, app, entryUUID, analysis.Entities)
}
// Best-effort SPO relationship extraction — runs in background because it makes an LLM call.
go func() {
    bgCtx := context.Background()
    ExtractAndStoreRelationships(bgCtx, app, entryUUID, content)
}()
```

- [ ] **Step 3: Build and run all tests**

```bash
go build ./...
go test ./... 2>&1 | tail -30
```

Expected: no failures. In particular: `go test ./internal/agent/...` must pass.

- [ ] **Step 4: Verify `LinkEntryToPeople` has no remaining callers**

```bash
grep -r "LinkEntryToPeople" /Users/jstrohm/code/jot --include="*.go"
```

Expected: only appears in `graph_builder.go` (where it is defined). If there are no callers, delete the function from `graph_builder.go`. If a caller exists, keep a thin wrapper and add a TODO to remove it.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/process_entry.go internal/agent/graph_builder.go
git commit -m "feat(agent): wire entity resolution and relationship extraction into ProcessEntry pipeline"
```

---

## Task 4: Update `app_capabilities.txt`

Per project rules, update `internal/prompts/app_capabilities.txt` to reflect new ingest behavior.

- [ ] **Step 1: Add to the agent/ingest section:**

```
- Ingest entity resolution: At write time, entity mentions in new journal entries are resolved to existing knowledge nodes (persons, places, orgs) and the entry is linked to them without waiting for the nightly Dreamer.
- Ingest relationship extraction: SPO triples (Subject | Predicate | Object) are extracted via LLM from new entries and stored as generic knowledge nodes with bidirectional links to subject and object entities.
```

- [ ] **Step 2: Commit**

```bash
git add internal/prompts/app_capabilities.txt
git commit -m "docs: update app_capabilities with ingest-time entity resolution and SPO extraction"
```

---

## Smoke Test (Manual)

```bash
# Start local server
./scripts/test-local.sh

# Log an entry with a relationship statement
curl -X POST http://localhost:8080/log \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $JOT_API_KEY" \
  -d '{"content": "Had lunch with Gloria today. She is excited about her new job at Anthropic."}'

# Check logs for:
# - "resolve_entities linked" — Gloria linked to entry
# - "relationship stored" — SPO node for "Gloria works_at Anthropic" created

# After ~5 seconds, query
curl -X POST http://localhost:8080/query \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $JOT_API_KEY" \
  -d '{"question": "Where does Gloria work?"}'
```

---

## Done Criteria

- [ ] `TestBuildRelationshipExtractor_ContainsContent` and `_EmptyContent` pass
- [ ] All 5 `TestParseSPOLines_*` tests pass
- [ ] `ResolveAndLinkEntities` handles all entity types (not just persons)
- [ ] `ExtractAndStoreRelationships` stores SPO nodes and links subject/object
- [ ] Both wired into `ProcessEntry` (resolve synchronous, relationship extraction as goroutine)
- [ ] `LinkEntryToPeople` removed (no callers remaining)
- [ ] `go build ./...` is clean
- [ ] `app_capabilities.txt` updated
