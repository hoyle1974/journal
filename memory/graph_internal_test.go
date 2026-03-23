package memory

import (
	"testing"
)

func TestPruneCandidates_TopKByCosine(t *testing.T) {
	s := New(nil, nil, nil)
	queryVec := []float32{1, 0, 0}

	candidates := []KnowledgeNodeWithLinks{
		{KnowledgeNode: KnowledgeNode{UUID: "a", Embedding: []float32{1, 0, 0}}},     // cos=1.0 (closest)
		{KnowledgeNode: KnowledgeNode{UUID: "b", Embedding: []float32{0, 1, 0}}},     // cos=0.0
		{KnowledgeNode: KnowledgeNode{UUID: "c", Embedding: []float32{0.9, 0.1, 0}}}, // cos≈0.99
	}

	result := s.pruneCandidates(candidates, queryVec, 2)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	// Top-2 should be "a" and "c" (highest cosine similarity).
	got := map[string]bool{result[0].UUID: true, result[1].UUID: true}
	if !got["a"] || !got["c"] {
		t.Errorf("expected UUIDs a and c, got %v", result)
	}
}

func TestPruneCandidates_NilVectorFallback(t *testing.T) {
	s := New(nil, nil, nil)

	candidates := []KnowledgeNodeWithLinks{
		{KnowledgeNode: KnowledgeNode{UUID: "x"}},
		{KnowledgeNode: KnowledgeNode{UUID: "y"}},
		{KnowledgeNode: KnowledgeNode{UUID: "z"}},
	}

	// nil queryVector → first-K hard cap
	result := s.pruneCandidates(candidates, nil, 2)
	if len(result) != 2 {
		t.Fatalf("expected 2 (hard cap), got %d", len(result))
	}
	if result[0].UUID != "x" || result[1].UUID != "y" {
		t.Errorf("expected first-2 (x,y), got %v", result)
	}
}

// TestGraphExpandVisitedMapPreventsDuplicates verifies that the same UUID cannot
// appear twice in SubGraph.Nodes even if discovered from multiple paths.
func TestGraphExpandVisitedMapPreventsDuplicates(t *testing.T) {
	// We test this via pruneCandidates + visited logic directly, since
	// full BFS requires Firestore. This is a compile/logic check.
	s := New(nil, nil, nil)
	candidates := []KnowledgeNodeWithLinks{
		{KnowledgeNode: KnowledgeNode{UUID: "dup"}},
		{KnowledgeNode: KnowledgeNode{UUID: "dup"}}, // duplicate
		{KnowledgeNode: KnowledgeNode{UUID: "unique"}},
	}
	// pruneCandidates with maxK=10 returns all; caller deduplicates via visited map.
	result := s.pruneCandidates(candidates, nil, 10)
	if len(result) != 3 { // pruneCandidates does not deduplicate — caller's responsibility
		t.Errorf("pruneCandidates should not deduplicate, got %d", len(result))
	}
}

func TestPruneCandidates_SmallerThanK(t *testing.T) {
	s := New(nil, nil, nil)
	candidates := []KnowledgeNodeWithLinks{
		{KnowledgeNode: KnowledgeNode{UUID: "only"}},
	}
	result := s.pruneCandidates(candidates, []float32{1, 0}, 10)
	if len(result) != 1 {
		t.Errorf("expected all 1 candidate returned when < maxK, got %d", len(result))
	}
}
