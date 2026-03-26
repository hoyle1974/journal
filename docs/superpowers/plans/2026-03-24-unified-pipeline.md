# Unified Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the separate `jot log` / `jot query` paths with a single synchronous pipeline: save entry → refinery → task worker → 2-hop Loom RAG → FOH with Gemini 2.5 native thinking.

**Architecture:** Every input (CLI or Telegram) follows one pipeline: the journal entry is saved, the refinery extracts graph triples and returns their node IDs, those IDs seed a 2-hop graph expansion for RAG context, then FOH runs with `ThinkingConfig{IncludeThoughts: true}` and the RAG context injected into the system prompt. Native thinking tokens replace the broken `<thought>` XML approach.

**Tech Stack:** Go, `google.golang.org/genai v1.49.0` (ThinkingConfig, Part.Thought), Firestore, Cloud Tasks (async save-query only).

---

## File Map

| File | Change |
|---|---|
| `internal/agent/refinery_pipeline.go` | `refineryResolveCommit` + `runRefineryPipeline` return `([]string, error)` |
| `internal/agent/process_entry.go` | `ProcessEntryReport` gets `ExtractedNodeIDs`; stage 4 removed; add `ProcessEntrySyncPipeline` |
| `internal/agent/loom_rag.go` | `BuildLoomRAGContext` takes seed node IDs; no vector search |
| `internal/agent/loom_workers.go` | Remove `runResponseWorker` (replaced by FOH) |
| `internal/agent/foh_thought.go` | Delete `extractThoughtsAndStrip`; keep `thoughtSuggestsKnowledgeGap`, `truncateThoughtForTrace` |
| `internal/agent/foh.go` | Use `ExtractThinkingAndAnswer`; add `ragContext` via `RunQueryFull` |
| `internal/agent/foh_helpers.go` | Add `AddEntryOnly` (save, no enqueue) |
| `internal/agent/prompter.go` | Add `LoomContextBlock` to `SystemPromptData`; pass through `BuildSystemPrompt` |
| `internal/prompts/prompts.go` | Add `LoomContextBlock string` to `SystemPromptData` |
| `internal/prompts/system_prompt.txt` | Remove REASONING PROTOCOL; add `{{.LoomContextBlock}}` slot |
| `internal/infra/chat.go` | `NewChatSession` accepts optional thinking; add `ExtractThinkingAndAnswer` |
| `internal/infra/gemini.go` | Remove silent 2.5-pro→flash downgrade |
| `internal/service/agent_service.go` | Add `ProcessAndRespond` method |
| `internal/api/backend.go` | Add `ProcessAndRespond` to `AgentService` interface |
| `internal/api/handler_interact.go` | Add `handleIngest`; keep `handleQuery` for backward compat |
| `internal/api/router.go` | Add `POST /ingest` route |
| `cmd/jot/main.go` | Remove `log`/`query` commands; default routes to `POST /ingest` |
| `internal/prompts/app_capabilities.txt` | Update to reflect new pipeline |

---

## Task 1: Brief + Worktree

- [ ] Create the brief file at `briefs/active/20260324_unified-pipeline.md` using the template at `briefs/TEMPLATE.md`
- [ ] Create the worktree and branch:
  ```bash
  git worktree add ../jot-unified-pipeline -b feature/unified-pipeline
  ```
- [ ] Verify worktree exists:
  ```bash
  git worktree list
  ```
- [ ] All subsequent edits happen under `../jot-unified-pipeline/`

---

## Task 2: Refinery returns extracted node IDs

**Files:**
- Modify: `internal/agent/refinery_pipeline.go`
- Test: `internal/agent/refinery_pipeline_test.go`

The refinery already creates subject, object, and relationship nodes but discards their UUIDs. We need to collect and return them so `BuildLoomRAGContext` knows exactly which nodes to seed from.

- [ ] **Write the failing test**

In `internal/agent/refinery_pipeline_test.go`, add:

```go
func TestRunRefineryPipelineReturnsNodeIDs(t *testing.T) {
    // nil app returns error, not panic, and empty IDs
    ids, err := runRefineryPipeline(context.Background(), nil, "uuid-1", "test content")
    if err == nil {
        t.Fatal("expected error for nil app")
    }
    if ids != nil {
        t.Fatalf("expected nil ids on error, got %v", ids)
    }
}
```

- [ ] **Run test to verify it fails**
  ```bash
  cd ../jot-unified-pipeline && go test ./internal/agent/ -run TestRunRefineryPipelineReturnsNodeIDs -v
  ```
  Expected: compile error (`runRefineryPipeline` returns `error` not `([]string, error)`)

- [ ] **Update `refineryResolveCommit` to return `([]string, error)`**

In `refinery_pipeline.go`, change signature and collect IDs:

```go
func refineryResolveCommit(ctx context.Context, app *infra.App, entryUUID string, triples []refineryTriple, canonMap memory.CanonicalMapConfig) ([]string, error) {
    ctx, span := infra.StartSpan(ctx, "agent.refinery_resolve_commit")
    defer span.End()
    span.SetAttributes(map[string]string{"entry_uuid": entryUUID})

    var nodeIDs []string
    for _, t := range triples {
        if t.ParseErr != "" {
            infra.LoggerFrom(ctx).Warn("refinery rejected triple", "entry_uuid", entryUUID, "reason", t.ParseErr, "raw_line", t.RawLine)
            continue
        }
        // ... (existing NEW: prefix handling and canonicalization unchanged) ...

        subj, err := app.Memory.EnsureNode(ctx, t.Subject, subType, entryUUID)
        if err != nil {
            infra.LoggerFrom(ctx).Warn("refinery ensure subject failed", ...)
            continue
        }
        obj, err := app.Memory.EnsureNode(ctx, t.Object, objType, entryUUID)
        if err != nil {
            infra.LoggerFrom(ctx).Warn("refinery ensure object failed", ...)
            continue
        }
        relID, err := app.Memory.CreateRelationshipNode(ctx, subj.UUID, predicate, obj.UUID, entryUUID, subj.Content, obj.Content)
        if err != nil {
            infra.LoggerFrom(ctx).Warn("refinery create relationship failed", ...)
            continue
        }
        // Collect all produced node IDs
        nodeIDs = append(nodeIDs, subj.UUID, obj.UUID, relID)

        // ... (existing backlink and hot-edge calls unchanged) ...
    }
    return nodeIDs, nil
}
```

- [ ] **Update `runRefineryPipeline` to surface the IDs**

```go
func runRefineryPipeline(ctx context.Context, app *infra.App, entryUUID, content string) ([]string, error) {
    // ... (existing span, canonMap fetch unchanged) ...
    triples, err := refineryExtract(ctx, app, entryUUID, content, canonMap)
    if err != nil {
        return nil, fmt.Errorf("refinery extract: %w", err)
    }
    if len(triples) == 0 {
        infra.LoggerFrom(ctx).Debug("refinery: no triples", "entry_uuid", entryUUID)
        return nil, nil
    }
    return refineryResolveCommit(ctx, app, entryUUID, triples, canonMap)
}
```

- [ ] **Run test to verify it passes**
  ```bash
  go test ./internal/agent/ -run TestRunRefineryPipelineReturnsNodeIDs -v
  ```

- [ ] **Run full agent tests to confirm no regressions**
  ```bash
  go test ./internal/agent/ -v
  ```

- [ ] **Commit**
  ```bash
  git add internal/agent/refinery_pipeline.go internal/agent/refinery_pipeline_test.go
  git commit -m "feat(refinery): return extracted node IDs from pipeline"
  ```

---

## Task 3: ProcessLogSequential surfaces node IDs, drops stage 4

**Files:**
- Modify: `internal/agent/process_entry.go`
- Test: `internal/agent/loom_test.go`

Stage 4 (response worker) is replaced by the new FOH+thinking step. We also need node IDs in the report so the caller can seed `BuildLoomRAGContext`.

- [ ] **Write the failing test**

In `internal/agent/loom_test.go`, add:

```go
func TestProcessLogSequentialReturnsNodeIDs(t *testing.T) {
    report, err := ProcessLogSequential(context.Background(), nil, "uuid-1", "content", "2026-01-01T00:00:00Z", "test")
    if err == nil {
        t.Fatal("expected error for nil app")
    }
    _ = report // nil on error is fine
}

func TestProcessEntryReportHasExtractedNodeIDs(t *testing.T) {
    r := &ProcessEntryReport{ExtractedNodeIDs: []string{"a", "b"}}
    if len(r.ExtractedNodeIDs) != 2 {
        t.Fatalf("expected 2 node IDs, got %d", len(r.ExtractedNodeIDs))
    }
}
```

- [ ] **Run test to verify it fails**
  ```bash
  go test ./internal/agent/ -run TestProcessEntryReportHasExtractedNodeIDs -v
  ```
  Expected: compile error (`ProcessEntryReport` has no `ExtractedNodeIDs` field)

- [ ] **Update `ProcessEntryReport`**

```go
type ProcessEntryReport struct {
    Content          string
    Source           string
    TaskCreated      string   // commitment intent if auto-created; empty if none
    ExtractedNodeIDs []string // subject, object, relationship UUIDs from refinery stage 2
}
```

- [ ] **Update `ProcessLogSequential`: collect node IDs, remove stage 4**

Replace the stage 2 block and remove stage 4:

```go
// ── Stage 2: Refinery ─────────────────────────────────────────────────────
var extractedNodeIDs []string
nodeIDs, refineryErr := runRefineryPipeline(ctx, app, logUUID, logContent)
if refineryErr != nil {
    infra.LoggerFrom(ctx).Warn("loom stage 2 FAILED: refinery pipeline error — pipeline continues",
        "log_uuid", logUUID, "error", refineryErr)
} else {
    extractedNodeIDs = nodeIDs
    infra.LoggerFrom(ctx).Info("loom stage 2 done: refinery complete",
        "log_uuid", logUUID, "node_count", len(extractedNodeIDs))
}

// ── Stage 3: Task Worker ──────────────────────────────────────────────────
taskErr := runTaskWorker(ctx, app, logContent, []string{logUUID})
// ... (existing logging unchanged) ...

// Stage 4 removed — FOH+thinking replaces response worker

return &ProcessEntryReport{
    Content:          utils.TruncateString(logContent, 500),
    Source:           source,
    ExtractedNodeIDs: extractedNodeIDs,
}, nil
```

- [ ] **Run tests**
  ```bash
  go test ./internal/agent/ -v
  ```

- [ ] **Add `ProcessEntrySyncPipeline` to `process_entry.go`**

  This is the function `ProcessAndRespond` calls — stages 2 and 3 only, skipping stage 1 (log persistence) because the entry was already saved by `AddEntryOnly`. This avoids the double-write that would occur if `ProcessLogSequential` (which runs stage 1 with `MergeAll`) were called after `AddEntryOnly`.

```go
// ProcessEntrySyncPipeline runs refinery (stage 2) and task worker (stage 3) for an entry
// that has already been persisted by the caller. Returns the node IDs extracted by the refinery.
// Use from the unified synchronous pipeline (ProcessAndRespond); use ProcessLogSequential for
// the async Cloud Task path.
func ProcessEntrySyncPipeline(ctx context.Context, app *infra.App, logUUID, logContent, source string) ([]string, error) {
    if app == nil || app.Config() == nil {
        return nil, fmt.Errorf("ProcessEntrySyncPipeline: app or config is nil")
    }
    ctx, span := infra.StartSpan(ctx, "loom.process_entry_sync")
    defer span.End()
    span.SetAttributes(map[string]string{"log_uuid": logUUID, "source": source})

    nodeIDs, refineryErr := runRefineryPipeline(ctx, app, logUUID, logContent)
    if refineryErr != nil {
        infra.LoggerFrom(ctx).Warn("sync pipeline: refinery failed", "log_uuid", logUUID, "error", refineryErr)
    }
    if taskErr := runTaskWorker(ctx, app, logContent, []string{logUUID}); taskErr != nil {
        infra.LoggerFrom(ctx).Warn("sync pipeline: task worker failed", "log_uuid", logUUID, "error", taskErr)
    }
    return nodeIDs, refineryErr
}
```

- [ ] **Commit**
  ```bash
  git add internal/agent/process_entry.go internal/agent/loom_test.go
  git commit -m "feat(loom): surface extracted node IDs from pipeline, add ProcessEntrySyncPipeline, remove stage 4"
  ```

---

## Task 4: Rewrite BuildLoomRAGContext with seed node IDs

**Files:**
- Modify: `internal/agent/loom_rag.go`
- Modify: `internal/agent/loom_workers.go` (remove `runResponseWorker`)

New contract: given the node IDs the refinery just produced, fetch them directly and expand 2 hops. Falls back gracefully when seeds are empty (e.g. refinery failed).

- [ ] **Write the failing test**

In `internal/agent/loom_test.go`, add:

```go
func TestBuildLoomRAGContextNilApp(t *testing.T) {
    ctx := context.Background()
    result, err := BuildLoomRAGContext(ctx, nil, "log-uuid", []string{"node-1"})
    if err == nil {
        t.Fatal("expected error for nil app")
    }
    _ = result
}

func TestBuildLoomRAGContextEmptySeeds(t *testing.T) {
    // Empty seeds with nil app: should return early, not panic
    ctx := context.Background()
    result, err := BuildLoomRAGContext(ctx, nil, "log-uuid", nil)
    // nil app still errors, but we verify no panic on empty seeds
    _ = result
    _ = err
}

func TestLoomRAGContextFormatForPromptEmpty(t *testing.T) {
    r := &LoomRAGContext{}
    if r.FormatForPrompt() != "" {
        t.Fatal("expected empty string for empty context")
    }
}
```

- [ ] **Run test to verify it fails**
  ```bash
  go test ./internal/agent/ -run TestBuildLoomRAGContextNilApp -v
  ```
  Expected: compile error (signature mismatch)

- [ ] **Rewrite `BuildLoomRAGContext` in `loom_rag.go`**

```go
// BuildLoomRAGContext performs 2-hop context retrieval starting from seed node IDs.
// seedNodeIDs are the subject/object/relationship UUIDs produced by the refinery for
// the current log entry. When empty (e.g. refinery failed), falls back to open tasks only.
func BuildLoomRAGContext(ctx context.Context, app *infra.App, logUUID string, seedNodeIDs []string) (*LoomRAGContext, error) {
    if app == nil {
        return nil, fmt.Errorf("BuildLoomRAGContext: app required")
    }
    ctx, span := infra.StartSpan(ctx, "loom.build_rag_context")
    defer span.End()

    result := &LoomRAGContext{}

    if len(seedNodeIDs) > 0 {
        client, err := app.Firestore(ctx)
        if err != nil {
            return result, fmt.Errorf("loom rag: firestore: %w", err)
        }
        col := client.Collection(memory.KnowledgeCollection)
        seenIDs := make(map[string]bool)

        for _, nodeID := range seedNodeIDs {
            if nodeID == "" || seenIDs[nodeID] {
                continue
            }
            seenIDs[nodeID] = true

            doc, err := col.Doc(nodeID).Get(ctx)
            if err != nil {
                infra.LoggerFrom(ctx).Warn("loom rag: fetch seed node failed", "node_id", nodeID, "error", err)
                continue
            }
            data := doc.Data()
            nodeType, _ := data["node_type"].(string)
            content := getStringFieldFromMap(data, "content")

            if nodeType == memory.NodeTypeRelationship || nodeType == "relationship" {
                // Relationship seed: record summary, then follow subject+object as second hop.
                subj, _ := data["subject_uuid"].(string)
                obj, _ := data["object_uuid"].(string)
                result.RelationshipSummaries = append(result.RelationshipSummaries,
                    fmt.Sprintf("[rel] %s | %s | subj=%s obj=%s", nodeID, content, subj, obj))
                for _, hopID := range []string{subj, obj} {
                    if hopID == "" || seenIDs[hopID] {
                        continue
                    }
                    seenIDs[hopID] = true
                    hopDoc, err := col.Doc(hopID).Get(ctx)
                    if err != nil {
                        continue
                    }
                    hopData := hopDoc.Data()
                    result.HopNodeSummaries = append(result.HopNodeSummaries,
                        fmt.Sprintf("[node] %s | %s", hopID, getStringFieldFromMap(hopData, "content")))
                    hotEdges, _ := hopData["hot_edges"].([]any)
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
                        result.HopNodeSummaries = append(result.HopNodeSummaries,
                            fmt.Sprintf("[hot-edge] %s | %s", heID, getStringFieldFromMap(heDoc.Data(), "content")))
                    }
                }
            } else {
                // Entity seed: record summary, then expand hot_edges as second hop.
                result.HopNodeSummaries = append(result.HopNodeSummaries,
                    fmt.Sprintf("[node] %s | %s", nodeID, content))
                hotEdges, _ := data["hot_edges"].([]any)
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
                    result.HopNodeSummaries = append(result.HopNodeSummaries,
                        fmt.Sprintf("[hot-edge] %s | %s", heID, getStringFieldFromMap(heDoc.Data(), "content")))
                }
            }
        }
    }

    // Always include open tasks for task-aware reasoning.
    tasks, err := app.Memory.GetOpenRootTasks(ctx, 10)
    if err != nil {
        infra.LoggerFrom(ctx).Warn("loom rag: fetch open tasks failed", "error", err)
    }
    for _, t := range tasks {
        result.PendingTaskSummaries = append(result.PendingTaskSummaries,
            fmt.Sprintf("[task] %s | status=%s | %s", t.UUID, t.Status, t.Content))
    }
    return result, nil
}
```

  Note: the `if nodeType == "relationship"` / `else` branch above has a structural error in the draft — the `else` belongs to the outer `if`. Clean up during implementation so the control flow is correct.

- [ ] **Remove `runResponseWorker` from `loom_workers.go`**

  Delete the `runResponseWorker` function entirely. It is replaced by the FOH+thinking step.

- [ ] **Run tests**
  ```bash
  go test ./internal/agent/ -v
  ```

- [ ] **Commit**
  ```bash
  git add internal/agent/loom_rag.go internal/agent/loom_workers.go internal/agent/loom_test.go
  git commit -m "feat(loom): seed BuildLoomRAGContext from refinery node IDs, remove response worker"
  ```

---

## Task 5: ThinkingConfig + ExtractThinkingAndAnswer in infra

**Files:**
- Modify: `internal/infra/chat.go`
- Test: `internal/infra/chat_test.go`

`GenerateContentConfig` in v1.49.0 has `ThinkingConfig *genai.ThinkingConfig`. `Part.Thought bool` marks thinking tokens. We expose this through `NewChatSession` and a new extraction helper.

- [ ] **Write the failing test**

In `internal/infra/chat_test.go`, add:

```go
func TestExtractThinkingAndAnswer_ThoughtPart(t *testing.T) {
    resp := &genai.GenerateContentResponse{
        Candidates: []*genai.Candidate{
            {
                Content: &genai.Content{
                    Parts: []*genai.Part{
                        {Text: "I should search for X", Thought: true},
                        {Text: "Here is the answer."},
                    },
                },
            },
        },
    }
    thinking, answer := ExtractThinkingAndAnswer(resp)
    if thinking != "I should search for X" {
        t.Errorf("thinking = %q, want %q", thinking, "I should search for X")
    }
    if answer != "Here is the answer." {
        t.Errorf("answer = %q, want %q", answer, "Here is the answer.")
    }
}

func TestExtractThinkingAndAnswer_NilResp(t *testing.T) {
    thinking, answer := ExtractThinkingAndAnswer(nil)
    if thinking != "" || answer != "" {
        t.Errorf("expected empty strings for nil resp")
    }
}

func TestExtractThinkingAndAnswer_OnlyFunctionCalls(t *testing.T) {
    resp := &genai.GenerateContentResponse{
        Candidates: []*genai.Candidate{
            {
                Content: &genai.Content{
                    Parts: []*genai.Part{
                        {FunctionCall: &genai.FunctionCall{Name: "semantic_search"}},
                    },
                },
            },
        },
    }
    thinking, answer := ExtractThinkingAndAnswer(resp)
    if thinking != "" || answer != "" {
        t.Errorf("expected empty strings when only function calls present")
    }
}
```

- [ ] **Run tests to verify they fail**
  ```bash
  go test ./internal/infra/ -run TestExtractThinkingAndAnswer -v
  ```
  Expected: compile error (`ExtractThinkingAndAnswer` undefined)

- [ ] **Add `ExtractThinkingAndAnswer` to `chat.go`**

```go
// ExtractThinkingAndAnswer splits a response into native thinking tokens and answer text.
// Parts with Thought==true are reasoning; all other text parts are the answer.
// Returns empty strings when the response is nil or contains only function calls.
func ExtractThinkingAndAnswer(resp *genai.GenerateContentResponse) (thinking, answer string) {
    if resp == nil {
        return "", ""
    }
    var thinkParts, answerParts []string
    for _, cand := range resp.Candidates {
        if cand == nil || cand.Content == nil {
            continue
        }
        for _, p := range cand.Content.Parts {
            if p == nil {
                continue
            }
            if p.Thought && p.Text != "" {
                thinkParts = append(thinkParts, p.Text)
            } else if !p.Thought && p.Text != "" {
                answerParts = append(answerParts, p.Text)
            }
        }
    }
    return strings.TrimSpace(strings.Join(thinkParts, "\n")),
        strings.TrimSpace(strings.Join(answerParts, "\n"))
}
```

- [ ] **Add `WithThinking bool` option to `NewChatSession`**

Change signature:
```go
func NewChatSession(ctx context.Context, app *App, systemPrompt string, tools []*genai.FunctionDeclaration, withThinking bool) (*ChatSession, error)
```

Inside, after building `config`:
```go
if withThinking {
    config.ThinkingConfig = &genai.ThinkingConfig{IncludeThoughts: true}
}
```

- [ ] **Fix all callers of `NewChatSession`** — search for them:
  ```bash
  grep -rn "NewChatSession" ../jot-unified-pipeline/
  ```
  Pass `false` for existing callers (FOH will pass `true` in Task 7).

- [ ] **Run tests**
  ```bash
  go test ./internal/infra/ -v
  ```

- [ ] **Commit**
  ```bash
  git add internal/infra/chat.go internal/infra/chat_test.go
  git commit -m "feat(infra): add ThinkingConfig support and ExtractThinkingAndAnswer helper"
  ```

---

## Task 7: Update FOH to use native thinking

> **Ordering note:** Implement **Task 6** (Inject LoomContextBlock) before this task. `RunQueryFull` calls `BuildSystemPrompt` with the new `ragContext` parameter, which is defined in Task 6. Implement Task 6 first so this compiles.

**Files:**
- Modify: `internal/agent/foh.go`
- Modify: `internal/agent/foh_thought.go` (remove extraction logic)
- Modify: `internal/prompts/system_prompt.txt`

Replace `<thought>` regex extraction with `ExtractThinkingAndAnswer`. Add `RunQueryFull` that accepts `ragContext`.

- [ ] **Write the failing test**

In `internal/agent/foh_thought_test.go`, verify `extractThoughtsAndStrip` is gone after the change by removing its tests (they'll fail to compile). Replace with:

```go
func TestThoughtSuggestsKnowledgeGap_StillWorks(t *testing.T) {
    // This function is kept for gap detection on thinking text.
    if !thoughtSuggestsKnowledgeGap("Identified gaps: missing last week data") {
        t.Fatal("expected gap detected")
    }
    if thoughtSuggestsKnowledgeGap("Identified gaps: none") {
        t.Fatal("expected no gap for 'none'")
    }
}
```

- [ ] **Remove `extractThoughtsAndStrip` from `foh_thought.go`**

  Delete the `extractThoughtsAndStrip` function and its regex `thoughtBlockRegex`. Keep `truncateThoughtForTrace` and `thoughtSuggestsKnowledgeGap`.

- [ ] **Add `RunQueryFull` to `foh.go`**

```go
// RunQueryFull is the full entry point used by the unified pipeline.
// ragContext is the Loom 2-hop graph context to inject into the system prompt; pass "" to skip.
func RunQueryFull(ctx context.Context, app FOHEnv, question, source string, debug bool, ragContext string) *QueryResult {
    // identical to RunQueryWithDebug body, with these changes:
    // 1. Pass ragContext to BuildSystemPrompt (via context value or direct param — see Task 6)
    // 2. NewChatSession(..., true) — enable thinking
    // 3. Replace extractThoughtsAndStrip with ExtractThinkingAndAnswer
}

// RunQueryWithDebug is preserved for backward compat; passes empty ragContext.
func RunQueryWithDebug(ctx context.Context, app FOHEnv, question, source string, debug bool) *QueryResult {
    return RunQueryFull(ctx, app, question, source, debug, "")
}
```

- [ ] **Update the FOH loop inside `RunQueryFull`**

Replace the thought-extraction block:
```go
// OLD:
fullModelText := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
th, stripped := extractThoughtsAndStrip(fullModelText)
if th != "" { ... }

// NEW:
thinking, answerText := infra.ExtractThinkingAndAnswer(resp)
if thinking != "" {
    if thoughtSuggestsKnowledgeGap(thinking) {
        knowledgeGapDetected = true
    }
    reasoningTrace = append(reasoningTrace, truncateThoughtForTrace(thinking))
    infra.LoggerFrom(ctx).Debug("FOH: thinking block", "query_run_id", queryRunID, "iter", iteration, "thinking_len", len(thinking))
    if debug {
        logDebug("[iter %d] thinking: %s", iteration, thinking)
    }
}
```

Replace all uses of `stripped` with `answerText`.

Update `NewChatSession` call:
```go
session, err := infra.NewChatSession(ctx, app.App(), systemPrompt, toolDefs, true)
```

- [ ] **Remove REASONING PROTOCOL from `system_prompt.txt`**

  Delete lines 12–23 (the `REASONING PROTOCOL` block). Native thinking replaces it. Keep everything else.

- [ ] **Run all agent tests**
  ```bash
  go test ./internal/agent/ -v
  ```

- [ ] **Commit**
  ```bash
  git add internal/agent/foh.go internal/agent/foh_thought.go internal/agent/foh_thought_test.go internal/prompts/system_prompt.txt
  git commit -m "feat(foh): replace <thought> extraction with Gemini 2.5 native thinking"
  ```

---

## Task 6: Inject LoomContextBlock into system prompt

**Files:**
- Modify: `internal/prompts/prompts.go`
- Modify: `internal/prompts/system_prompt.txt`
- Modify: `internal/agent/prompter.go`

`BuildSystemPrompt` needs to accept and inject the Loom RAG context. We add `LoomContextBlock` to `SystemPromptData` and pass it from the FOH.

- [ ] **Add `LoomContextBlock` to `SystemPromptData` in `prompts.go`**

```go
type SystemPromptData struct {
    // ... existing fields ...
    LoomContextBlock string // 2-hop RAG context from refinery; empty string = omit section
}
```

- [ ] **Add the conditional template block to `system_prompt.txt`**

  After the `{{.ActiveProjectBlock}}` line (line 87), add:
  ```
  {{if .LoomContextBlock}}
  ---
  ## GRAPH CONTEXT (from this entry)
  # Nodes and relationships extracted from the current input and their 2-hop neighbours.

  {{.LoomContextBlock}}
  {{end}}
  ```
  The header lives in the template — `LoomContextBlock` carries only the formatted RAG content (no string-concatenated header in Go code).

- [ ] **Update `BuildSystemPrompt` in `prompter.go` to accept ragContext**

Change signature:
```go
func BuildSystemPrompt(ctx context.Context, env infra.ToolEnv, ragContext string) (string, error)
```

Inside, wrap `ragContext` as user data if non-empty (header is already in the template):
```go
loomContextBlock := ""
if ragContext != "" {
    loomContextBlock = utils.WrapAsUserData(ragContext)
}
promptData := prompts.SystemPromptData{
    // ... existing fields ...
    LoomContextBlock: loomContextBlock,
}
```

- [ ] **Fix the one caller of `BuildSystemPrompt` in `foh.go`**

  In `RunQueryFull`, pass `ragContext`:
  ```go
  systemPrompt, err := BuildSystemPrompt(ctx, app, ragContext)
  ```

- [ ] **Run tests**
  ```bash
  go test ./internal/agent/ ./internal/prompts/ -v
  ```

- [ ] **Commit**
  ```bash
  git add internal/prompts/prompts.go internal/prompts/system_prompt.txt internal/agent/prompter.go internal/agent/foh.go
  git commit -m "feat(prompt): inject Loom RAG context block into system prompt"
  ```

---

## Task 8: Fix silent model downgrade in gemini.go

**Files:**
- Modify: `internal/infra/gemini.go`

- [ ] **Write the failing test**

In `internal/infra/chat_test.go` (or a new `gemini_test.go`), add:

```go
func TestResolveModelDoesNotDowngradePro(t *testing.T) {
    // 2.5-pro should not silently become flash
    available := []string{"gemini-2.5-pro", "gemini-2.5-flash"}
    result := resolveModel("gemini-2.5-pro", available)
    if result != "gemini-2.5-pro" {
        t.Errorf("resolveModel downgraded 2.5-pro to %q, want %q", result, "gemini-2.5-pro")
    }
}
```

- [ ] **Run test to verify it fails**
  ```bash
  go test ./internal/infra/ -run TestResolveModelDoesNotDowngradePro -v
  ```

- [ ] **Remove the silent downgrade from `resolveModel`**

  Delete lines 121–123:
  ```go
  // DELETE:
  if strings.Contains(configured, "2.5-pro") {
      use = "gemini-2.5-flash"
  }
  ```

- [ ] **Run test to verify it passes**
  ```bash
  go test ./internal/infra/ -run TestResolveModelDoesNotDowngradePro -v
  ```

- [ ] **Commit**
  ```bash
  git add internal/infra/gemini.go internal/infra/chat_test.go
  git commit -m "fix(gemini): remove silent 2.5-pro to flash model downgrade"
  ```

---

## Task 9: AddEntryOnly + ProcessAndRespond service method

**Files:**
- Modify: `internal/agent/foh_helpers.go`
- Modify: `internal/service/agent_service.go`
- Modify: `internal/api/backend.go`

`ProcessAndRespond` runs the full sync pipeline: save entry → refinery+tasks → Loom RAG → FOH with thinking.

- [ ] **Export `errQueryResult` → `ErrQueryResult` in `foh.go`**

  Search for the unexported `errQueryResult` function:
  ```bash
  grep -n "errQueryResult" ../jot-unified-pipeline/internal/agent/foh.go
  ```
  Rename it to `ErrQueryResult` (capital E). Update all internal callers in the same file. This makes it callable as `agent.ErrQueryResult(...)` from `agent_service.go`.

- [ ] **Add `AddEntryOnly` to `foh_helpers.go`**

```go
// AddEntryOnly saves the journal entry and returns its UUID without enqueueing async processing.
// Use for the unified synchronous pipeline where processing happens inline.
func AddEntryOnly(ctx context.Context, app *infra.App, content, source string, timestamp *string, imageURL string) (string, error) {
    if app == nil {
        return "", fmt.Errorf("app required for AddEntryOnly")
    }
    return app.Memory.AddEntry(ctx, content, source, timestamp, imageURL)
}
```

- [ ] **Add `ProcessAndRespond` to `agent_service.go`**

```go
// ProcessAndRespond runs the unified synchronous pipeline for a user input:
// save entry → refinery + task worker → 2-hop Loom RAG → FOH with native thinking.
func (a *AgentService) ProcessAndRespond(ctx context.Context, input, source string) *api.QueryResult {
    infra.LoggerFrom(ctx).Info("function call", "fn", "ProcessAndRespond", "source", source, "input_len", len(input))

    // 1. Save entry (no enqueue — pipeline runs synchronously below).
    ts := time.Now().Format(time.RFC3339)
    entryUUID, err := agent.AddEntryOnly(ctx, a.app, input, source, &ts, "")
    if err != nil {
        infra.LoggerFrom(ctx).Error("ProcessAndRespond: save entry failed", "error", err)
        return queryResultToAPI(agent.ErrQueryResult("Error saving entry: "+err.Error(), 0, nil, nil))
    }

    // 2. Refinery + task worker (stages 2-3 only — entry already persisted by AddEntryOnly above).
    // ProcessEntrySyncPipeline skips stage 1 (MergeAll) to avoid a double-write.
    nodeIDs, err := agent.ProcessEntrySyncPipeline(ctx, a.app, entryUUID, input, source)
    if err != nil {
        infra.LoggerFrom(ctx).Warn("ProcessAndRespond: pipeline failed (continuing to FOH)", "error", err)
    }

    // 3. Build 2-hop Loom RAG context from just-extracted nodes.
    ragCtx, err := agent.BuildLoomRAGContext(ctx, a.app, entryUUID, nodeIDs)
    if err != nil {
        infra.LoggerFrom(ctx).Warn("ProcessAndRespond: loom RAG failed (continuing without context)", "error", err)
    }
    ragContext := ""
    if ragCtx != nil {
        ragContext = ragCtx.FormatForPrompt()
    }

    // 4. FOH with native thinking + RAG context.
    // WithEntryAlreadyAdded tells FOH the entry is already saved — skip duplicate write.
    fohCtx := agent.WithEntryAlreadyAdded(ctx, entryUUID)
    result := agent.RunQueryFull(fohCtx, a.app, input, source, false, ragContext)
    infra.LoggerFrom(ctx).Info("function result", "fn", "ProcessAndRespond",
        "error", result.Error, "iterations", result.Iterations,
        "has_thinking", len(result.ReasoningTrace) > 0)
    return queryResultToAPI(result)
}
```

- [ ] **Add `ProcessAndRespond` to the `AgentService` interface in `backend.go`**

```go
type AgentService interface {
    AddEntry(ctx context.Context, content, source string, timestamp *string, imageBytes []byte) (string, error)
    RunQuery(ctx context.Context, question, source string) *QueryResult
    ProcessAndRespond(ctx context.Context, input, source string) *QueryResult
    ProcessLogSequential(ctx context.Context, logUUID, logContent, timestamp, source string) (*agent.ProcessEntryReport, error)
}
```

- [ ] **Run tests**
  ```bash
  go test ./internal/service/ ./internal/api/ -v
  ```

- [ ] **Commit**
  ```bash
  git add internal/agent/foh_helpers.go internal/service/agent_service.go internal/api/backend.go
  git commit -m "feat(service): add ProcessAndRespond unified sync pipeline method"
  ```

---

## Task 10: POST /ingest handler + route

**Files:**
- Modify: `internal/api/handler_interact.go`
- Modify: `internal/api/router.go`

- [ ] **Add `handleIngest` to `handler_interact.go`**

```go
// handleIngest is the unified handler for all user input (facts, questions, commands).
// Runs the full synchronous pipeline: save → refinery → Loom RAG → FOH with thinking.
func handleIngest(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
    ctx := r.Context()
    var data struct {
        Content string `json:"content" validate:"required"`
        Source  string `json:"source"`
    }
    if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
        return nil, handlerError(http.StatusBadRequest, err.Error())
    }
    content := strings.TrimSpace(data.Content)
    source := data.Source
    if source == "" {
        source = "api"
    }
    LogHandlerRequest(ctx, r.Method, pathForLog(r.URL.Path),
        "content_preview", utils.TruncateString(content, 80), "source", source)
    result := s.Agent.ProcessAndRespond(ctx, content, source)
    return result, nil
}
```

- [ ] **Add route to `router.go`**

Inside the protected routes group, add:
```go
r.Post("/ingest", wrapAPI(handleIngest))
```

- [ ] **Run tests**
  ```bash
  go test ./internal/api/ -v
  ```

- [ ] **Commit**
  ```bash
  git add internal/api/handler_interact.go internal/api/router.go
  git commit -m "feat(api): add POST /ingest unified pipeline handler"
  ```

---

## Task 11: CLI — unified `jot <text>`

**Files:**
- Modify: `cmd/jot/main.go`

Remove `log`/`l`/`query`/`q` as named subcommands. The default case (all unrecognized input) already calls `cmdQuery` — redirect it to call `POST /ingest` instead. Keep `edit`, `entries`, `help`.

- [ ] **Add `cmdIngest` function**

```go
func cmdIngest(input string) {
    result, headers, err := api.Do(context.Background(), "POST", "/ingest", map[string]string{
        "content": input,
        "source":  fmt.Sprintf("cli:%s", MachineName),
    }, time.Duration(timeout.QuerySeconds)*time.Second)
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        os.Exit(1)
    }
    if result == nil {
        fmt.Println("ok")
        return
    }
    if traceFlag && headers != nil {
        printTraceInfo(headers)
    }
    // Print reasoning trace if present
    printReasoningTraceIfAny(result)
    // Print answer or fallback to "ok"
    if answer := jsonStr(result, "answer"); answer != "" {
        fmt.Println(answer)
    } else {
        fmt.Println("ok")
    }
}
```

- [ ] **Update `main()` switch**

  - Remove `case "log", "l":` block
  - Remove `case "query", "q":` block
  - In the `default:` case, replace `cmdQuery(input)` with `cmdIngest(input)`
  - Add a direct `jot <text>` case that joins all args and calls `cmdIngest`:

```go
default:
    input := strings.Join(args, " ")
    cmdIngest(input)
```

- [ ] **Update help text** in `cmdHelp` to remove log/query topics and describe the new unified usage:

```
jot <anything>

Just type to your assistant:
  jot Had coffee with Sarah
  jot What did I do last week?
  jot I want to learn Japanese
```

- [ ] **Manual smoke test** (requires running server):
  ```bash
  cd ../jot-unified-pipeline && go build ./cmd/jot/
  ./jot "test entry from unified pipeline"
  ```
  Expected: reasoning trace (if thinking tokens returned) + answer or "ok"

- [ ] **Commit**
  ```bash
  git add cmd/jot/main.go
  git commit -m "feat(cli): unified jot <text> input, remove log/query subcommands"
  ```

---

## Task 12: Update docs + closeout

**Files:**
- Modify: `internal/prompts/app_capabilities.txt`
- Modify: `briefs/active/20260324_unified-pipeline.md` (session log)

- [ ] **Update `app_capabilities.txt`**

  Replace the COT section:
  - Remove reference to `<thought>` blocks
  - Add: "All input runs a synchronous pipeline: save entry → refinery (Subject/Predicate/Object graph extraction) → task worker → 2-hop Loom RAG → FOH with Gemini 2.5 native thinking. Reasoning tokens are returned as `reasoning_trace` in the response."

- [ ] **Run full test suite**
  ```bash
  go test ./... 2>&1 | tail -20
  ```
  Expected: all pass or known-skip (integration tests requiring Firestore emulator)

- [ ] **Build to confirm no compile errors**
  ```bash
  go build ./...
  ```

- [ ] **Append session log to brief**

- [ ] **Merge and close out**
  ```bash
  cd /Users/jstrohm/code/jot
  git checkout main
  git merge feature/unified-pipeline
  git worktree remove ../jot-unified-pipeline
  mv briefs/active/20260324_unified-pipeline.md briefs/done/
  ```

- [ ] **Final commit on main**
  ```bash
  git add briefs/done/20260324_unified-pipeline.md internal/prompts/app_capabilities.txt
  git commit -m "docs: update app_capabilities for unified pipeline, close brief"
  ```
