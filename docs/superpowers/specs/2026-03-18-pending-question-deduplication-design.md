# Pending Question Deduplication

**Date:** 2026-03-18
**Status:** Approved

## Problem

The Dreamer's nightly gap detection creates `pending_questions` without checking whether similar questions already exist. Over multiple nights, the same gap can be re-detected and re-queued, flooding the pending queue with near-duplicate questions and degrading the user experience.

## Goal

Prevent duplicate or semantically similar questions from entering the `pending_questions` collection, and give the gap detector LLM upstream awareness of questions already waiting for answers.

## Approach: Dual-Layer Deduplication

1. **Embedding-based filter at insert time** — catches duplicates after generation
2. **Prompt injection** — prevents re-generation of similar questions upstream

---

## Section 1: Data Model Change

**File:** `pkg/memory/pending.go`

Add an `embedding` field to `PendingQuestion`:

```go
Embedding []float32 `firestore:"embedding,omitempty"`
```

Embedded at creation using `infra.GenerateEmbedding(ctx, env.Config().GoogleCloudProject, text, infra.EmbedTaskRetrievalDocument)`. Both stored questions and incoming candidates use `EmbedTaskRetrievalDocument` — this is the correct task type for pairwise document-to-document similarity comparisons (matching the pattern in `mergeDreamerFacts`).

**Write path:** The write map in `InsertPendingQuestions` must include `"embedding": q.Embedding` alongside existing fields.

**Read path:** Existing read functions (`GetUnresolvedPendingQuestions`, new `GetRecentlyResolvedPendingQuestions`) manually extract fields from `doc.Data()` using `infra.GetStringField` / `infra.GetStringSliceField`. A new helper `infra.GetFloat32SliceField(data map[string]interface{}, field string) []float32` must be added to `internal/infra/firestore.go` to handle Firestore's `[]interface{}` → `[]float32` conversion. Both read functions must call this helper to populate `q.Embedding`.

---

## Section 2: Insert-Time Deduplication

**File:** `pkg/memory/pending.go`

New function: `filterDuplicatePendingQuestions(ctx context.Context, env infra.ToolEnv, candidates []PendingQuestion) ([]PendingQuestion, error)`

`infra.ToolEnv` exposes `Config() *config.Config`, so `env.Config().GoogleCloudProject` provides the project ID for embedding calls. No additional parameters needed.

### Logic

1. Fetch existing comparison set:
   - Unresolved: `GetUnresolvedPendingQuestions(ctx, env, 100)` — note: the Firestore query has a hardcoded `Limit(100)`; the `limit` arg is a post-fetch slice cap. Passing 100 matches the Firestore scan limit.
   - Resolved within 30 days: `GetRecentlyResolvedPendingQuestions(ctx, env, time.Now().AddDate(0, 0, -30))`
   - Combined, capped at 200 total
2. Embed all candidate question texts in a single batch via `infra.GenerateEmbeddingsBatch(ctx, projectID, texts, infra.EmbedTaskRetrievalDocument)`
3. Store each embedding on the candidate struct
4. For each candidate, compute `pkg/utils.CosineSimilarity(candidate.Embedding, existing.Embedding)` against each existing question's stored `Embedding` — O(candidates × existing), at most ~200×200 = 40,000 comparisons on 768-dim vectors; acceptable at these scales
5. If any existing question scores ≥ **0.85**, drop the candidate; log at Info level with the matched question text
6. Return only non-duplicate candidates

**Failure mode:** If the embedding API call fails, log a warning and return all candidates unfiltered — dedup is best-effort; the nightly cron must not silently fail to write questions due to a transient API error.

`InsertPendingQuestions` calls `filterDuplicatePendingQuestions` before any Firestore writes. If all candidates are filtered, returns early with no writes.

### New query function

`GetRecentlyResolvedPendingQuestions(ctx context.Context, env infra.ToolEnv, since time.Time) ([]PendingQuestion, error)`

- Server-side: `Where("created_at", ">=", since.Format(time.RFC3339))`, ordered by `created_at` descending, `Limit(200)` — server-side range filter guarantees completeness within the 30-day window
- Client-side: filters to `resolved_at != ""`
- Uses the automatic single-field `created_at` index (no new composite index entry in `firestore.indexes.json` required)

### Cosine similarity extraction

`cosineSimilarity` is currently unexported in `internal/agent/dreamer.go`. Extract to `pkg/utils` as:

```go
// CosineSimilarity returns the cosine similarity between two float32 vectors.
func CosineSimilarity(a, b []float32) float64
```

Both call sites in `dreamer.go` must be updated: the pairwise comparison in `mergeDreamerFacts` and any other local uses. `pkg/memory` importing `pkg/utils` creates no import cycle (confirmed: `pkg/utils` does not import `pkg/memory` or `internal/agent`).

---

## Section 3: Gap Detector Prompt Injection

**Files:** `internal/agent/dreamer_synthesis.go`, `internal/prompts/prompts.go`, `internal/prompts/gap_detector.txt`

### Runtime change (`dreamer_synthesis.go`)

Before building the Gemini prompt in `RunGapDetection()`:

1. Fetch up to 20 unresolved pending questions: `GetUnresolvedPendingQuestions(ctx, app, 20)` (separate call from the dedup filter's limit-100 call)
2. Join their question texts into a newline-separated bullet list string
3. Apply `utils.SanitizePrompt` then `utils.WrapAsUserData` at the call site — matching the existing pattern for `RecentJournal`: `utils.WrapAsUserData(utils.SanitizePrompt(questionsBlock))`
4. Pass the pre-wrapped string as `PendingQuestionsBlock string` on `GapDetectorData`

If there are no unresolved questions, pass an empty string; the template renders nothing.

### Struct change (`prompts.go`)

Add field to `GapDetectorData`:

```go
type GapDetectorData struct {
    RecentJournal         string
    RelevantKnowledge     string
    ToolManifest          string
    PendingQuestionsBlock string // SanitizePrompt+WrapAsUserData-wrapped bullet list; empty string if none
}
```

**Deployment constraint:** The struct field addition and the `gap_detector.txt` template change must land in the same commit. The `template.Must(template.New(...).Parse(...))` at `init()` panics at startup on any struct/template mismatch, so the two files are tightly coupled.

### Prompt change (`gap_detector.txt`)

Add a new section rendered only when the field is non-empty:

```
{{if .PendingQuestionsBlock}}
## Questions Already Waiting for an Answer
The following questions have already been queued and are awaiting the user's response.
Do NOT propose questions that are the same as or closely related to these:

{{.PendingQuestionsBlock}}
{{end}}
```

---

## Similarity Threshold & Window

| Threshold | Behavior |
|-----------|----------|
| ≥ 0.85 | Block — clearly the same question, possibly different phrasing |
| < 0.85 | Allow — related topic but distinct enough angle |

| Questions checked | Rationale |
|-------------------|-----------|
| All unresolved (limit 100) | Never re-ask something still waiting |
| Resolved within 30 days | Avoid re-asking something just answered |
| Resolved > 30 days ago | Allowed — topic may legitimately resurface |
| Combined cap: 200 docs | Prevents unbounded reads |

---

## Files Changed

| File | Change |
|------|--------|
| `pkg/memory/pending.go` | Add `Embedding` field to struct; add `"embedding"` to write map; update read paths to call `infra.GetFloat32SliceField`; add `filterDuplicatePendingQuestions`; add `GetRecentlyResolvedPendingQuestions`; update `InsertPendingQuestions` to call filter |
| `internal/infra/firestore.go` | Add `GetFloat32SliceField(data map[string]interface{}, field string) []float32` helper |
| `pkg/utils/similarity.go` | New file: export `CosineSimilarity(a, b []float32) float64` |
| `internal/agent/dreamer.go` | Remove local `cosineSimilarity`; update both call sites to `utils.CosineSimilarity` |
| `internal/agent/dreamer_synthesis.go` | Fetch 20 unresolved; join + SanitizePrompt + WrapAsUserData; pass as `PendingQuestionsBlock` |
| `internal/prompts/prompts.go` | Add `PendingQuestionsBlock string` to `GapDetectorData` (must co-commit with gap_detector.txt) |
| `internal/prompts/gap_detector.txt` | Add conditional `{{if .PendingQuestionsBlock}}` section (must co-commit with prompts.go) |

---

## Out of Scope

- Deduplication of `queries` (user Q&A log) — historical records, not actionable items
- Retroactive dedup of existing pending questions — a separate admin concern
- FOH gap detection — creates `queries` with `is_gap=true`, not `pending_questions`
