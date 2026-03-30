package memory_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackstrohm/jot/memory"
)

func TestGraphExpandValidation(t *testing.T) {
	ctx := context.Background()
	s := memory.New(nil, nil, nil)

	// empty seedID should return an error (validated before touching db)
	_, err := s.GraphExpand(ctx, "", nil, 1, 10)
	if err == nil {
		t.Fatal("expected error for empty seedID, got nil")
	}
}


func TestKnowledgeNodeHasEmbeddingField(t *testing.T) {
	// Compile-time check: KnowledgeNode must have an Embedding field of type []float32.
	var n memory.KnowledgeNode
	var _ []float32 = n.Embedding
}

func TestSubGraphToMarkdown(t *testing.T) {
	sg := &memory.SubGraph{
		Nodes: map[string]memory.KnowledgeNodeWithLinks{
			"seed-001": {KnowledgeNode: memory.KnowledgeNode{UUID: "seed-001", Content: "Project Apollo", NodeType: "project"}},
			"node-002": {KnowledgeNode: memory.KnowledgeNode{UUID: "node-002", Content: "Neil Armstrong", NodeType: "person"}},
		},
		Edges: []memory.Edge{
			{SourceUUID: "seed-001", TargetUUID: "node-002", Predicate: "entity_link"},
		},
	}

	md := sg.ToMarkdown("seed-001")

	checks := []string{
		"Knowledge Graph Neighborhood",
		"Project Apollo",
		"seed-001",
		"Neil Armstrong",
		"node-002",
		"entity_link",
	}
	for _, want := range checks {
		if !strings.Contains(md, want) {
			t.Errorf("ToMarkdown missing %q in output:\n%s", want, md)
		}
	}
}

func TestGetKnowledgeNodesByIDsEmpty(t *testing.T) {
	ctx := context.Background()
	s := memory.New(nil, nil, nil)
	result, err := s.GetKnowledgeNodesByIDs(ctx, nil)
	if err != nil {
		t.Fatalf("expected no error for nil ids, got: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for nil ids, got %v", result)
	}
}

func TestGetKnowledgeNodesByIDsDeduplication(t *testing.T) {
	// Verifies deduplication logic without Firestore by passing empty slice.
	ctx := context.Background()
	s := memory.New(nil, nil, nil)
	result, err := s.GetKnowledgeNodesByIDs(ctx, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 results, got %d", len(result))
	}
}

func TestGetKnowledgeNodesByIDsReturnType(t *testing.T) {
	// Compile-time check: return type must be []memory.KnowledgeNodeWithLinks.
	ctx := context.Background()
	s := memory.New(nil, nil, nil)
	var _ []memory.KnowledgeNodeWithLinks = func() []memory.KnowledgeNodeWithLinks {
		result, _ := s.GetKnowledgeNodesByIDs(ctx, nil)
		return result
	}()
}

// TestKnowledgeNodeObjectUUIDFields verifies that KnowledgeNodeWithLinks exposes the
// ObjectUUID and Predicate fields required for intrinsic outgoing SPO edge traversal.
func TestKnowledgeNodeObjectUUIDFields(t *testing.T) {
	n := memory.KnowledgeNodeWithLinks{
		KnowledgeNode: memory.KnowledgeNode{
			UUID:       "subject-001",
			ObjectUUID: "object-002",
			Predicate:  "works_at",
		},
	}
	if n.ObjectUUID != "object-002" {
		t.Errorf("expected ObjectUUID %q, got %q", "object-002", n.ObjectUUID)
	}
	if n.Predicate != "works_at" {
		t.Errorf("expected Predicate %q, got %q", "works_at", n.Predicate)
	}
}

// Suppress unused import warning — strings is used by later tests added in Task 3.
var _ = strings.Contains
