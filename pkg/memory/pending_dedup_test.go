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
