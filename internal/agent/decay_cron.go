package agent

import (
	"context"
	"fmt"
	"math"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
)

const (
	// decayLambda controls the exponential decay rate.
	// At λ=0.05 a node loses ~39% of its score after 10 days and ~92% after 60 days.
	decayLambda = 0.05

	// decayMinScore is the floor below which relevance_score is not written back,
	// avoiding churn on near-zero values.
	decayMinScore = 0.01

	// decayPageSize is the number of documents fetched per pagination batch.
	decayPageSize = 100
)

// decayNodeTypes are the node types subject to nightly relevance decay.
var decayNodeTypes = []string{memory.NodeTypeRelationship, memory.NodeTypeObject}

// ApplyNightlyDecay iterates all relationship and object nodes and applies
// exponential relevance decay:
//
//	score = current_score * exp(-λ * days_since_last_update)
//
// Nodes with no existing relevance_score are skipped.
// Nodes that fall below decayMinScore are floored at decayMinScore.
// Score changes smaller than 0.001 are skipped to avoid noisy writes.
//
// TODO(loom/phase-6): Edge Reshuffle — after decay, identify cold relationship nodes
// (relevance_score < coldEdgeThreshold) that have fallen below hot candidates.
// Swap them out of their parent node's hot_edges array and replace with hotter candidates.
// Requires a secondary pass over object nodes comparing scores across their hot_edges.
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
		processed, updated, skipped, typeErr := decayNodeType(ctx, client, nodeType, now)
		if typeErr != nil {
			infra.LoggerFrom(ctx).Warn("ApplyNightlyDecay: error processing node type",
				"node_type", nodeType, "error", typeErr)
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
	return nil
}

// decayNodeType applies exponential decay to all nodes of a given type, paginating results.
func decayNodeType(ctx context.Context, client *firestore.Client, nodeType string, now time.Time) (processed, updated, skipped int, err error) {
	baseQuery := client.Collection(memory.KnowledgeCollection).
		Where("node_type", "==", nodeType).
		OrderBy(firestore.DocumentID, firestore.Asc).
		Limit(decayPageSize)

	var lastDocSnap *firestore.DocumentSnapshot

	for {
		q := baseQuery
		if lastDocSnap != nil {
			q = q.StartAfter(lastDocSnap)
		}
		iter := q.Documents(ctx)
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

			// Determine the reference time for decay. Prefer the stored timestamp; fall back to
			// Firestore document update time so nodes that predate the Loom migration still decay.
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
			// Skip write if change is negligible.
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
			break // Last page for this node type.
		}
	}
	return processed, updated, skipped, nil
}

// applyDecay computes score * exp(-λ * Δdays).
func applyDecay(score, daysDelta float64) float64 {
	return score * math.Exp(-decayLambda*daysDelta)
}
