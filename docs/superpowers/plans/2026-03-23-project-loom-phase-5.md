# Project Loom — Phase 5: Nightly Decay Cron

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a nightly maintenance cron stub that applies exponential relevance decay to all `relationship` and `object` nodes and includes a comment stub for future hot-edge reshuffling.

**Architecture:** `ApplyNightlyDecay` iterates all nodes of the relevant types via Firestore pagination, applies `score * exp(-λ * Δt)` where λ=0.05 and Δt is days since last update, and writes the new score back. The function is not wired to a scheduler in this phase — that is a follow-up task.

**Tech Stack:** Go 1.22+, Firestore SDK, `math`, `time`, `internal/infra`, `memory` package

**Prerequisites:** Plan `2026-03-23-project-loom-phases-1-2.md` complete (needs `RelevanceScore` on `KnowledgeNode`).

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/agent/decay_cron.go` | `ApplyNightlyDecay` and decay math helpers |

---

## Task 1: Implement `ApplyNightlyDecay`

**Files:**
- Create: `internal/agent/decay_cron.go`

- [ ] **Step 1: Write the file**

```go
package agent

import (
	"context"
	"fmt"
	"math"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/hoyle1974/memory"
)

const (
	// decayLambda controls the exponential decay rate.
	// At λ=0.05 a node loses ~39% of its score after 10 days.
	decayLambda = 0.05

	// decayMinScore is the floor below which scores are not written back
	// (avoids churning Firestore with near-zero updates).
	decayMinScore = 0.01

	// decayPageSize is the number of documents fetched per iteration page.
	decayPageSize = 100
)

// decayNodeTypes are the node types subject to nightly decay.
var decayNodeTypes = []string{memory.NodeTypeRelationship, memory.NodeTypeObject}

// ApplyNightlyDecay iterates all relationship and object nodes and applies
// exponential relevance decay: score = current_score * exp(-λ * days_since_update).
//
// Nodes with no existing relevance_score are skipped (score treated as 0, no update needed).
// Nodes that fall below decayMinScore are set to decayMinScore (floor) to avoid deletion noise.
//
// STUB: Edge Reshuffle is not yet implemented. See comment below.
func ApplyNightlyDecay(ctx context.Context, app *infra.App) error {
	ctx, span := infra.StartSpan(ctx, "loom.apply_nightly_decay")
	defer span.End()

	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("ApplyNightlyDecay: firestore client: %w", err)
	}

	now := time.Now().UTC()
	var totalProcessed, totalUpdated, totalSkipped int

	for _, nodeType := range decayNodeTypes {
		processed, updated, skipped, err := decayNodeType(ctx, client, nodeType, now)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("ApplyNightlyDecay: error processing node type",
				"node_type", nodeType, "error", err)
			// Continue to next type rather than aborting.
		}
		totalProcessed += processed
		totalUpdated += updated
		totalSkipped += skipped
	}

	infra.LoggerFrom(ctx).Info("ApplyNightlyDecay complete",
		"total_processed", totalProcessed,
		"total_updated", totalUpdated,
		"total_skipped", totalSkipped,
	)

	// TODO(loom/phase-6): Edge Reshuffle — after decay, identify cold relationship nodes
	// (relevance_score < coldEdgeThreshold) whose scores have crossed below those of hot
	// candidates in the graph. Swap them out of their parent node's hot_edges array and
	// replace with hotter candidates. This requires a secondary pass over object nodes.

	return nil
}

// decayNodeType applies decay to all nodes of a given type, paginating through results.
func decayNodeType(ctx context.Context, client *firestore.Client, nodeType string, now time.Time) (processed, updated, skipped int, err error) {
	query := client.Collection(memory.KnowledgeCollection).
		Where("node_type", "==", nodeType).
		OrderBy(firestore.DocumentID, firestore.Asc).
		Limit(decayPageSize)

	var lastDocSnap *firestore.DocumentSnapshot

	for {
		if lastDocSnap != nil {
			query = query.StartAfter(lastDocSnap)
		}
		iter := query.Documents(ctx)
		batchCount := 0

		for {
			doc, nextErr := iter.Next()
			if nextErr == iterator.Done {
				break
			}
			if nextErr != nil {
				iter.Stop()
				return processed, updated, skipped, fmt.Errorf("decayNodeType(%s): iterate: %w", nodeType, nextErr)
			}
			lastDocSnap = doc
			batchCount++
			processed++

			data := doc.Data()
			currentScore, ok := data["relevance_score"].(float64)
			if !ok || currentScore <= 0 {
				skipped++
				continue
			}

			// Parse last-update timestamp. Fall back to document update time.
			lastUpdate := doc.UpdateTime
			if ts, ok := data["timestamp"].(string); ok && ts != "" {
				if t, parseErr := time.Parse(time.RFC3339, ts); parseErr == nil {
					lastUpdate = t
				}
			}

			daysDelta := now.Sub(lastUpdate).Hours() / 24
			if daysDelta <= 0 {
				skipped++
				continue
			}

			newScore := applyDecay(currentScore, daysDelta)
			if newScore < decayMinScore {
				newScore = decayMinScore
			}
			// Skip writing if score change is negligible (< 0.001).
			if math.Abs(newScore-currentScore) < 0.001 {
				skipped++
				continue
			}

			if _, updateErr := doc.Ref.Update(ctx, []firestore.Update{
				{Path: "relevance_score", Value: newScore},
			}); updateErr != nil {
				infra.LoggerFrom(ctx).Warn("decayNodeType: update failed",
					"doc_id", doc.Ref.ID,
					"node_type", nodeType,
					"error", updateErr,
				)
				skipped++
				continue
			}
			updated++
		}
		iter.Stop()

		if batchCount < decayPageSize {
			// Last page — done with this node type.
			break
		}
	}
	return processed, updated, skipped, nil
}

// applyDecay computes score * exp(-λ * Δdays).
func applyDecay(score, daysDelta float64) float64 {
	return score * math.Exp(-decayLambda*daysDelta)
}
```

- [ ] **Step 2: Verify compile**

```bash
cd ../jot-project-loom && go build ./internal/agent/...
```

Expected: no errors.

- [ ] **Step 3: Write unit tests for `applyDecay`**

Create `internal/agent/decay_cron_test.go`:

```go
package agent

import (
	"math"
	"testing"
)

func TestApplyDecay(t *testing.T) {
	tests := []struct {
		name      string
		score     float64
		days      float64
		wantRange [2]float64 // [min, max] acceptable output
	}{
		{"zero days no change", 0.8, 0, [2]float64{0.799, 0.801}},
		{"one day small decay", 1.0, 1, [2]float64{0.95, 0.96}},  // exp(-0.05) ≈ 0.951
		{"ten days significant decay", 1.0, 10, [2]float64{0.60, 0.62}}, // exp(-0.5) ≈ 0.607
		{"low score stays above zero", 0.05, 100, [2]float64{0.0, 0.01}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyDecay(tt.score, tt.days)
			if got < tt.wantRange[0] || got > tt.wantRange[1] {
				t.Errorf("applyDecay(%v, %v) = %v, want in [%v, %v]",
					tt.score, tt.days, got, tt.wantRange[0], tt.wantRange[1])
			}
		})
	}
}

func TestApplyDecayLambda(t *testing.T) {
	// Verify λ=0.05: after 10 days score should be approx 0.607 of original.
	score := 1.0
	days := 10.0
	want := math.Exp(-decayLambda * days) // ≈ 0.6065
	got := applyDecay(score, days)
	if math.Abs(got-want) > 0.0001 {
		t.Errorf("applyDecay(1.0, 10) = %v, want %v", got, want)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
cd ../jot-project-loom && go test ./internal/agent/... -run TestApplyDecay -v
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
cd ../jot-project-loom
git add internal/agent/decay_cron.go internal/agent/decay_cron_test.go
git commit -m "feat(loom/phase5): implement ApplyNightlyDecay with exponential relevance decay"
```

---

## Task 2: Final Verification

- [ ] `go build ./...` — clean
- [ ] `go test ./...` — no regressions
- [ ] `go vet ./...` — clean

---

## Future Work (Not In Scope)

- Wire `ApplyNightlyDecay` to the existing cron infrastructure (Cloud Scheduler → HTTP endpoint → handler).
- Implement Edge Reshuffle pass (noted in TODO comment in `decay_cron.go`).
- Add Firestore composite index for `node_type + relevance_score` to support efficient cold-edge queries during reshuffle.
