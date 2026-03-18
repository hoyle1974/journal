# Pending Question Deduplication

**Date:** 2026-03-18
**Status:** Approved

## Problem

The Dreamer's nightly gap detection creates `pending_questions` without checking whether similar questions already exist. Over multiple nights, the same gap can be re-detected and re-queued, flooding the pending queue with near-duplicate questions and degrading the user experience.

## Goal

Prevent duplicate or semantically similar questions from entering the `pending_questions` collection, and give the gap detector LLM upstream awareness of questions already waiting for answers.

## Approach: Dual-Layer Deduplication (C)

Two complementary mechanisms:

1. **Embedding-based filter at insert time** — catches duplicates after generation
2. **Prompt injection** — prevents re-generation of similar questions upstream

---

## Section 1: Data Model Change

**File:** `pkg/memory/pending.go`

Add an `embedding` field to `PendingQuestion`:

```go
Embedding []float32 `firestore:"embedding,omitempty"`
```

When a `PendingQuestion` is created, its question text is embedded using the existing `infra.EmbedText` infrastructure and stored on the struct before writing to Firestore. This allows future similarity comparisons without re-embedding at query time.

No Firestore index changes required — the 30-day window filter uses the existing `created_at` field and is applied client-side.

---

## Section 2: Insert-Time Deduplication

**File:** `pkg/memory/pending.go`

New function: `filterDuplicatePendingQuestions(ctx, app, candidates []PendingQuestion) ([]PendingQuestion, error)`

### Logic

1. Fetch existing pending questions: all unresolved **or** resolved within the last 30 days
2. For each candidate:
   a. Embed the question text via `infra.EmbedText`
   b. Store the embedding on the candidate struct
   c. Compute cosine similarity against each existing question's stored embedding
   d. If any existing question scores ≥ **0.85**, drop the candidate
   e. Log dropped candidates at Info level, including the matching question text (for observability)
3. Return only non-duplicate candidates

`InsertPendingQuestions` calls this filter before any Firestore writes. If all candidates are filtered, it returns early with no writes.

### Reuse

The cosine similarity helper is already present in `pkg/memory` (used for knowledge node dedup). No new utility needed.

---

## Section 3: Gap Detector Prompt Injection

**Files:** `internal/agent/dreamer_synthesis.go`, `internal/prompts/gap_detector.txt`

### Runtime change (`dreamer_synthesis.go`)

Before building the Gemini prompt in `RunGapDetection()`:

1. Fetch up to 20 unresolved pending questions (newest first) via the existing `GetUnresolvedPendingQuestions`
2. Extract question text into a `[]string`
3. Pass to the gap detector prompt template via a new `PendingQuestions []string` field on the template data struct

### Prompt change (`gap_detector.txt`)

Add a new section to the prompt template:

```
## Questions Already Waiting for Your Answer
The following questions have already been queued and are awaiting the user's response.
Do NOT propose questions that are the same as or closely related to these:
{{range .PendingQuestions}}- {{.}}
{{end}}
```

This section is omitted (or rendered empty) when there are no pending questions.

---

## Similarity Threshold

| Threshold | Behavior |
|-----------|----------|
| ≥ 0.85 | Block — clearly the same question, possibly different phrasing |
| < 0.85 | Allow — related topic but distinct enough angle |

---

## Deduplication Window

| Questions checked | Rationale |
|-------------------|-----------|
| All unresolved | Never re-ask something still waiting |
| Resolved within 30 days | Avoid re-asking something just answered |
| Resolved > 30 days ago | Allowed — topic may legitimately resurface |

---

## Files Changed

| File | Change |
|------|--------|
| `pkg/memory/pending.go` | Add `Embedding` field; add `filterDuplicatePendingQuestions`; update `InsertPendingQuestions` |
| `internal/agent/dreamer_synthesis.go` | Fetch unresolved questions; pass to prompt template |
| `internal/prompts/gap_detector.txt` | Add pending questions section |

---

## Out of Scope

- Deduplication of `queries` (user Q&A log) — those are historical records, not actionable items
- Retroactive deduplication of existing pending questions — a separate admin concern
- FOH gap detection — FOH creates `queries` with `is_gap=true`, not `pending_questions`; separate system
