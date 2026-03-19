# Pending Question Deduplication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent duplicate or semantically similar questions from flooding `pending_questions` by adding embedding-based deduplication at insert time and injecting existing pending questions into the gap detector prompt.

**Architecture:** Extract `CosineSimilarity` to `pkg/utils`, add an embedding field to `PendingQuestion`, filter candidates before writing by comparing against stored embeddings, and pass the pending question list into the `GapDetectorData` template so the LLM avoids re-proposing existing questions.

**Tech Stack:** Go, Cloud Firestore, Vertex AI text-embedding-005 (`infra.GenerateEmbeddingsBatch`), `text/template`

**Worktree:** `/Users/jstrohm/code/jot-question-dedup` (branch `feature/question-dedup`)

> All file edits must be made inside the worktree path `/Users/jstrohm/code/jot-question-dedup/`.

---

## File Map

| File | Status | Responsibility |
|------|--------|----------------|
| `pkg/utils/similarity.go` | **Create** | Export `CosineSimilarity(a, b []float32) float64` |
| `pkg/utils/similarity_test.go` | **Create** | Unit tests for `CosineSimilarity` |
| `internal/infra/firestore.go` | **Modify** | Add `GetFloat32SliceField` helper |
| `internal/agent/dreamer.go` | **Modify** | Replace local `cosineSimilarity` with `utils.CosineSimilarity` |
| `pkg/memory/pending.go` | **Modify** | Add `Embedding` field; update write map; update read paths; add `GetRecentlyResolvedPendingQuestions`; add `filterDuplicatePendingQuestions`; update `InsertPendingQuestions` |
| `internal/prompts/prompts.go` | **Modify** | Add `PendingQuestionsBlock string` to `GapDetectorData` |
| `internal/prompts/gap_detector.txt` | **Modify** | Add conditional pending-questions section (must co-commit with prompts.go) |
| `internal/agent/dreamer_synthesis.go` | **Modify** | Fetch unresolved questions, build wrapped block, pass to template |

---

## Task 1: Extract `CosineSimilarity` to `pkg/utils`

**Files:**
- Create: `pkg/utils/similarity.go`
- Create: `pkg/utils/similarity_test.go`
- Modify: `internal/agent/dreamer.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/utils/similarity_test.go`:

```go
package utils

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
		tol  float64
	}{
		{
			name: "identical vectors",
			a:    []float32{1, 0, 0},
			b:    []float32{1, 0, 0},
			want: 1.0,
			tol:  1e-9,
		},
		{
			name: "orthogonal vectors",
			a:    []float32{1, 0},
			b:    []float32{0, 1},
			want: 0.0,
			tol:  1e-9,
		},
		{
			name: "opposite vectors",
			a:    []float32{1, 0},
			b:    []float32{-1, 0},
			want: -1.0,
			tol:  1e-9,
		},
		{
			name: "mismatched lengths",
			a:    []float32{1, 2},
			b:    []float32{1},
			want: 0.0,
			tol:  1e-9,
		},
		{
			name: "empty vectors",
			a:    []float32{},
			b:    []float32{},
			want: 0.0,
			tol:  1e-9,
		},
		{
			name: "zero vector",
			a:    []float32{0, 0},
			b:    []float32{1, 0},
			want: 0.0,
			tol:  1e-9,
		},
		{
			name: "45 degree angle",
			a:    []float32{1, 0},
			b:    []float32{1, 1},
			want: 1.0 / math.Sqrt2,
			tol:  1e-6,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CosineSimilarity(tc.a, tc.b)
			if math.Abs(got-tc.want) > tc.tol {
				t.Errorf("CosineSimilarity(%v, %v) = %v, want %v (±%v)", tc.a, tc.b, got, tc.want, tc.tol)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go test ./pkg/utils/... -run TestCosineSimilarity -v
```

Expected: compile error — `utils.CosineSimilarity` undefined.

- [ ] **Step 3: Create `pkg/utils/similarity.go`**

```go
package utils

import "math"

// CosineSimilarity returns the cosine similarity between two float32 vectors.
// Returns 0 if either vector is empty, zero-length, or the lengths differ.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go test ./pkg/utils/... -run TestCosineSimilarity -v
```

Expected: PASS, all subtests green.

- [ ] **Step 5: Replace local `cosineSimilarity` in `internal/agent/dreamer.go`**

In `internal/agent/dreamer.go`, the function `cosineSimilarity` at line 67 is used exactly once in `mergeDreamerFacts` at line 145. `pkg/utils` is already imported. Make two edits:

1. Delete the local `cosineSimilarity` function (lines 67–81)
2. Replace the single call `cosineSimilarity(flat[i].vec, flat[j].vec)` with `utils.CosineSimilarity(flat[i].vec, flat[j].vec)`

- [ ] **Step 6: Verify the build compiles**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go build ./...
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
cd /Users/jstrohm/code/jot-question-dedup && git add pkg/utils/similarity.go pkg/utils/similarity_test.go internal/agent/dreamer.go && git commit -m "refactor: extract CosineSimilarity to pkg/utils"
```

---

## Task 2: Add `GetFloat32SliceField` to `internal/infra/firestore.go`

**Files:**
- Modify: `internal/infra/firestore.go`

Firestore stores `[]float32` as `[]interface{}` where each element is a `float64`. The existing helper pattern (`GetStringSliceField`) is the model to follow.

- [ ] **Step 1: Write the test**

Add to `internal/infra/firestore_test.go` (create file if it doesn't exist):

```go
package infra_test

import (
	"testing"

	"github.com/jackstrohm/jot/internal/infra"
)

func TestGetFloat32SliceField(t *testing.T) {
	tests := []struct {
		name  string
		data  map[string]interface{}
		field string
		want  []float32
	}{
		{
			name:  "happy path",
			data:  map[string]interface{}{"v": []interface{}{float64(1.0), float64(0.5), float64(-0.25)}},
			field: "v",
			want:  []float32{1.0, 0.5, -0.25},
		},
		{
			name:  "missing field",
			data:  map[string]interface{}{},
			field: "v",
			want:  nil,
		},
		{
			name:  "wrong type",
			data:  map[string]interface{}{"v": "notanarray"},
			field: "v",
			want:  nil,
		},
		{
			name:  "empty slice",
			data:  map[string]interface{}{"v": []interface{}{}},
			field: "v",
			want:  []float32{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := infra.GetFloat32SliceField(tc.data, tc.field)
			if len(got) != len(tc.want) {
				t.Fatalf("got len=%d want len=%d; got=%v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %v want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go test ./internal/infra/... -run TestGetFloat32SliceField -v
```

Expected: compile error — `infra.GetFloat32SliceField` undefined.

- [ ] **Step 3: Add the helper to `internal/infra/firestore.go`**

Append after the existing `GetStringSliceField` function (around line 77):

```go
// GetFloat32SliceField parses a Firestore array of float64 (stored as []interface{}) into []float32.
func GetFloat32SliceField(data map[string]interface{}, field string) []float32 {
	v, ok := data[field].([]interface{})
	if !ok {
		return nil
	}
	out := make([]float32, 0, len(v))
	for _, e := range v {
		if f, ok := e.(float64); ok {
			out = append(out, float32(f))
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go test ./internal/infra/... -run TestGetFloat32SliceField -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/jstrohm/code/jot-question-dedup && git add internal/infra/firestore.go internal/infra/firestore_test.go && git commit -m "feat: add GetFloat32SliceField helper to infra"
```

---

## Task 3: Update `PendingQuestion` model and write path

**Files:**
- Modify: `pkg/memory/pending.go`

- [ ] **Step 1: Add `Embedding` field to the struct**

In `pkg/memory/pending.go`, the `PendingQuestion` struct currently ends at line 32. Add the field:

```go
type PendingQuestion struct {
	UUID           string    `firestore:"-" json:"uuid"`
	Question       string    `firestore:"question" json:"question"`
	Kind           string    `firestore:"kind" json:"kind"`
	Context        string    `firestore:"context" json:"context,omitempty"`
	SourceEntryIDs []string  `firestore:"source_entry_ids" json:"source_entry_ids,omitempty"`
	CreatedAt      string    `firestore:"created_at" json:"created_at"`
	ResolvedAt     string    `firestore:"resolved_at" json:"resolved_at,omitempty"`
	Answer         string    `firestore:"answer" json:"answer,omitempty"`
	Embedding      []float32 `firestore:"embedding,omitempty" json:"-"`
}
```

- [ ] **Step 2: Add `"embedding"` to the write map in `InsertPendingQuestions`**

The `Set()` call at line 55 uses a `map[string]interface{}`. Add the embedding key:

```go
_, err = client.Collection(PendingQuestionsCollection).Doc(q.UUID).Set(ctx, map[string]interface{}{
    "question":         q.Question,
    "kind":             q.Kind,
    "context":          q.Context,
    "source_entry_ids": q.SourceEntryIDs,
    "created_at":       q.CreatedAt,
    "resolved_at":      q.ResolvedAt,
    "answer":           q.Answer,
    "embedding":        q.Embedding,
})
```

- [ ] **Step 3: Update `GetUnresolvedPendingQuestions` read path to populate `Embedding`**

Inside the `mapDoc` function in `GetUnresolvedPendingQuestions` (around line 112), add the embedding read after `SourceEntryIDs`:

```go
q := PendingQuestion{
    UUID:       doc.Ref.ID,
    Question:   infra.GetStringField(data, "question"),
    Kind:       infra.GetStringField(data, "kind"),
    Context:    infra.GetStringField(data, "context"),
    CreatedAt:  infra.GetStringField(data, "created_at"),
    ResolvedAt: infra.GetStringField(data, "resolved_at"),
    Answer:     infra.GetStringField(data, "answer"),
    Embedding:  infra.GetFloat32SliceField(data, "embedding"),
}
q.SourceEntryIDs = infra.GetStringSliceField(data, "source_entry_ids")
```

- [ ] **Step 4: Verify build**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go build ./...
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
cd /Users/jstrohm/code/jot-question-dedup && git add pkg/memory/pending.go && git commit -m "feat: add Embedding field to PendingQuestion; update write/read paths"
```

---

## Task 4: Add `GetRecentlyResolvedPendingQuestions`

**Files:**
- Modify: `pkg/memory/pending.go`

- [ ] **Step 1: Add the function**

After `GetUnresolvedPendingQuestions`, add:

```go
// GetRecentlyResolvedPendingQuestions returns pending questions resolved after `since`, newest first.
// Used by the dedup filter to avoid re-asking recently answered questions.
// Scans at most 200 documents server-side (created_at DESC); client-side filters resolved_at != "".
func GetRecentlyResolvedPendingQuestions(ctx context.Context, env infra.ToolEnv, since time.Time) ([]PendingQuestion, error) {
	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	sinceStr := since.Format(time.RFC3339)
	query := client.Collection(PendingQuestionsCollection).
		Where("created_at", ">=", sinceStr).
		OrderBy("created_at", firestore.Desc).
		Limit(200)
	out, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (PendingQuestion, error) {
		data := doc.Data()
		if infra.GetStringField(data, "resolved_at") == "" {
			return PendingQuestion{}, fmt.Errorf("skip")
		}
		q := PendingQuestion{
			UUID:       doc.Ref.ID,
			Question:   infra.GetStringField(data, "question"),
			Kind:       infra.GetStringField(data, "kind"),
			Context:    infra.GetStringField(data, "context"),
			CreatedAt:  infra.GetStringField(data, "created_at"),
			ResolvedAt: infra.GetStringField(data, "resolved_at"),
			Answer:     infra.GetStringField(data, "answer"),
			Embedding:  infra.GetFloat32SliceField(data, "embedding"),
		}
		q.SourceEntryIDs = infra.GetStringSliceField(data, "source_entry_ids")
		return q, nil
	})
	if err != nil {
		return nil, infra.WrapFirestoreIndexError(err)
	}
	return out, nil
}
```

Note: the `Where("created_at", ">=", sinceStr).OrderBy("created_at", Desc)` query uses a range filter and sort on the same field. Firestore handles this with its automatic single-field index — no composite index entry in `firestore.indexes.json` is needed. If Firestore returns a `FailedPrecondition` index error in practice, `WrapFirestoreIndexError` will surface a clear message with the remediation step.

- [ ] **Step 2: Verify build**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
cd /Users/jstrohm/code/jot-question-dedup && git add pkg/memory/pending.go && git commit -m "feat: add GetRecentlyResolvedPendingQuestions for dedup window"
```

---

## Task 5: Implement `filterDuplicatePendingQuestions` and wire into `InsertPendingQuestions`

**Files:**
- Modify: `pkg/memory/pending.go`

This is the core dedup logic. It embeds incoming candidates, compares against existing questions, and drops near-duplicates.

- [ ] **Step 1: Write unit tests for the filter logic**

The filter calls the embedding API and Firestore, so a pure unit test focuses on the comparison logic in isolation. Add `pkg/memory/pending_dedup_test.go`:

```go
package memory_test

import (
	"testing"

	"github.com/jackstrohm/jot/pkg/utils"
)

// TestCosineSimilarityThreshold verifies that our chosen threshold (0.85)
// correctly separates near-identical questions from distinct ones.
// Uses pkg/utils.CosineSimilarity directly since filterDuplicatePendingQuestions
// depends on external services (Firestore, embedding API).
func TestCosineSimilarityThreshold(t *testing.T) {
	threshold := 0.85

	// Simulate two "nearly identical" embedding vectors (high similarity).
	a := []float32{0.9, 0.1, 0.0, 0.3}
	b := []float32{0.91, 0.09, 0.01, 0.29}
	sim := utils.CosineSimilarity(a, b)
	if sim < threshold {
		t.Errorf("expected near-identical vectors to exceed threshold %.2f, got %.4f", threshold, sim)
	}

	// Simulate two distinct vectors (low similarity).
	c := []float32{1, 0, 0, 0}
	d := []float32{0, 1, 0, 0}
	sim2 := utils.CosineSimilarity(c, d)
	if sim2 >= threshold {
		t.Errorf("expected orthogonal vectors to be below threshold %.2f, got %.4f", threshold, sim2)
	}
}
```

- [ ] **Step 2: Run test to verify it passes (no implementation needed — pure logic test)**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go test ./pkg/memory/... -run TestCosineSimilarityThreshold -v
```

Expected: PASS.

- [ ] **Step 3: Add `filterDuplicatePendingQuestions` to `pkg/memory/pending.go`**

Add after `GetRecentlyResolvedPendingQuestions`. Also add `"github.com/jackstrohm/jot/pkg/utils"` to the imports in `pending.go`.

```go
const dedupSimilarityThreshold = 0.85

// filterDuplicatePendingQuestions removes candidates that are semantically similar
// to existing pending questions (unresolved or resolved within 30 days).
// If the embedding API fails, all candidates are returned unfiltered (best-effort).
func filterDuplicatePendingQuestions(ctx context.Context, env infra.ToolEnv, candidates []PendingQuestion) ([]PendingQuestion, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}

	// Fetch the comparison set.
	unresolved, err := GetUnresolvedPendingQuestions(ctx, env, 100)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("dedup: failed to fetch unresolved questions, skipping dedup", "error", err)
		return candidates, nil
	}
	since := time.Now().AddDate(0, 0, -30)
	resolved, err := GetRecentlyResolvedPendingQuestions(ctx, env, since)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("dedup: failed to fetch resolved questions, skipping dedup", "error", err)
		return candidates, nil
	}

	existing := make([]PendingQuestion, 0, len(unresolved)+len(resolved))
	existing = append(existing, unresolved...)
	existing = append(existing, resolved...)
	if len(existing) > 200 {
		existing = existing[:200]
	}
	if len(existing) == 0 {
		return candidates, nil
	}

	// Embed all candidates in one batch.
	projectID := env.Config().GoogleCloudProject
	texts := make([]string, len(candidates))
	for i, c := range candidates {
		texts[i] = c.Question
	}
	vecs, err := infra.GenerateEmbeddingsBatch(ctx, projectID, texts, infra.EmbedTaskRetrievalDocument)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("dedup: embedding failed, inserting all candidates unfiltered", "error", err)
		return candidates, nil
	}
	for i := range candidates {
		candidates[i].Embedding = vecs[i]
	}

	// Compare each candidate against every existing question.
	var kept []PendingQuestion
	for _, c := range candidates {
		duplicate := false
		for _, ex := range existing {
			if len(ex.Embedding) == 0 {
				continue // no stored embedding; can't compare
			}
			sim := utils.CosineSimilarity(c.Embedding, ex.Embedding)
			if sim >= dedupSimilarityThreshold {
				infra.LoggerFrom(ctx).Info("dedup: dropping similar question",
					"candidate", c.Question,
					"matched", ex.Question,
					"similarity", sim,
				)
				duplicate = true
				break
			}
		}
		if !duplicate {
			kept = append(kept, c)
		}
	}
	return kept, nil
}
```

- [ ] **Step 4: Update `InsertPendingQuestions` to call the filter**

Replace the entire `InsertPendingQuestions` function body:

```go
func InsertPendingQuestions(ctx context.Context, env infra.ToolEnv, questions []PendingQuestion) error {
	if len(questions) == 0 {
		return nil
	}
	if env == nil {
		return fmt.Errorf("env required")
	}

	// Filter out duplicates before writing.
	filtered, err := filterDuplicatePendingQuestions(ctx, env, questions)
	if err != nil {
		return fmt.Errorf("filter duplicate questions: %w", err)
	}
	if len(filtered) == 0 {
		return nil
	}
	questions = filtered

	client, err := env.Firestore(ctx)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	for i := range questions {
		q := &questions[i]
		if q.UUID == "" {
			q.UUID = infra.GenerateUUID()
		}
		if q.CreatedAt == "" {
			q.CreatedAt = now
		}
		_, err = client.Collection(PendingQuestionsCollection).Doc(q.UUID).Set(ctx, map[string]interface{}{
			"question":         q.Question,
			"kind":             q.Kind,
			"context":          q.Context,
			"source_entry_ids": q.SourceEntryIDs,
			"created_at":       q.CreatedAt,
			"resolved_at":      q.ResolvedAt,
			"answer":           q.Answer,
			"embedding":        q.Embedding,
		})
		if err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: Verify build**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
cd /Users/jstrohm/code/jot-question-dedup && git add pkg/memory/pending.go pkg/memory/pending_dedup_test.go && git commit -m "feat: add filterDuplicatePendingQuestions with embedding-based dedup"
```

---

## Task 6: Prompt injection — update `GapDetectorData` and `gap_detector.txt`

**Files:**
- Modify: `internal/prompts/prompts.go`
- Modify: `internal/prompts/gap_detector.txt`

These two files **must be committed together** because `template.Must` at `init()` panics at startup on a struct/template field mismatch.

- [ ] **Step 1: Add `PendingQuestionsBlock` to `GapDetectorData` in `prompts.go`**

In `internal/prompts/prompts.go`, the `GapDetectorData` struct at line 193 currently has three fields. Add the new field:

```go
// GapDetectorData holds recent journal, relevant knowledge, tool manifest, and pending questions for gap detection.
type GapDetectorData struct {
	RecentJournal         string
	RelevantKnowledge     string
	ToolManifest          string
	PendingQuestionsBlock string // SanitizePrompt+WrapAsUserData-wrapped bullet list; empty string if none
}
```

- [ ] **Step 2: Add the conditional section to `gap_detector.txt`**

Append to the end of `internal/prompts/gap_detector.txt`:

```
{{if .PendingQuestionsBlock}}
## Questions Already Waiting for an Answer
The following questions have already been queued and are awaiting the user's response.
Do NOT propose questions that are the same as or closely related to these:

{{.PendingQuestionsBlock}}
{{end}}
```

- [ ] **Step 3: Verify build (tests the `template.Must` parse)**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go build ./...
```

Expected: no errors. If there's a template parse panic, the struct and template are out of sync — fix the template.

- [ ] **Step 4: Commit both files together**

```bash
cd /Users/jstrohm/code/jot-question-dedup && git add internal/prompts/prompts.go internal/prompts/gap_detector.txt && git commit -m "feat: add PendingQuestionsBlock to gap detector prompt template"
```

---

## Task 7: Wire prompt injection into `RunGapDetection`

**Files:**
- Modify: `internal/agent/dreamer_synthesis.go`

- [ ] **Step 1: Fetch unresolved questions and build the wrapped block**

In `internal/agent/dreamer_synthesis.go`, the `BuildGapDetector` call currently at line 55 looks like:

```go
userPrompt, err := prompts.BuildGapDetector(prompts.GapDetectorData{
    RecentJournal:     utils.WrapAsUserData(utils.SanitizePrompt(journalContext)),
    RelevantKnowledge: utils.WrapAsUserData(relevantKnowledge),
    ToolManifest:      utils.WrapAsUserData(capabilitiesAndTools),
})
```

Before this call, add the pending questions fetch and block construction:

```go
// Build the pending questions block for upstream dedup awareness.
pendingQs, pErr := memory.GetUnresolvedPendingQuestions(ctx, app, 20)
var pendingQuestionsBlock string
if pErr != nil {
    infra.LoggerFrom(ctx).Warn("gap detection: failed to fetch pending questions", "error", pErr)
} else if len(pendingQs) > 0 {
    lines := make([]string, len(pendingQs))
    for i, q := range pendingQs {
        lines[i] = "- " + q.Question
    }
    pendingQuestionsBlock = utils.WrapAsUserData(utils.SanitizePrompt(strings.Join(lines, "\n")))
}

userPrompt, err := prompts.BuildGapDetector(prompts.GapDetectorData{
    RecentJournal:         utils.WrapAsUserData(utils.SanitizePrompt(journalContext)),
    RelevantKnowledge:     utils.WrapAsUserData(relevantKnowledge),
    ToolManifest:          utils.WrapAsUserData(capabilitiesAndTools),
    PendingQuestionsBlock: pendingQuestionsBlock,
})
```

`strings` is already imported. `memory` is already imported. No new imports needed.

- [ ] **Step 2: Verify build**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run all tests**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go test ./... 2>&1 | head -60
```

Expected: all existing tests pass; new tests pass.

- [ ] **Step 4: Commit**

```bash
cd /Users/jstrohm/code/jot-question-dedup && git add internal/agent/dreamer_synthesis.go && git commit -m "feat: inject pending questions into gap detector prompt"
```

---

## Task 8: Final verification and merge

- [ ] **Step 1: Full build and test**

```bash
cd /Users/jstrohm/code/jot-question-dedup && go build ./... && go test ./...
```

Expected: clean build, all tests pass.

- [ ] **Step 2: Review git log for this branch**

```bash
cd /Users/jstrohm/code/jot-question-dedup && git log main..HEAD --oneline
```

Expected: 7 commits covering Tasks 1–7.

- [ ] **Step 3: Merge to main**

```bash
cd /Users/jstrohm/code/jot && git merge feature/question-dedup
```

- [ ] **Step 4: Remove the worktree**

```bash
git worktree remove /Users/jstrohm/code/jot-question-dedup
```

- [ ] **Step 5: Verify final build on main**

```bash
cd /Users/jstrohm/code/jot && go build ./... && go test ./...
```
