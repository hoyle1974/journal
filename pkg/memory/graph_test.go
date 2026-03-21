package memory_test

import (
	"context"
	"testing"

	"github.com/jackstrohm/jot/pkg/memory"
)

func TestGraphExpandValidation(t *testing.T) {
	ctx := context.Background()
	s := memory.New(nil, nil, nil)

	// empty seedID should return an error (validated before touching db)
	_, err := s.GraphExpand(ctx, "", 1, 10)
	if err == nil {
		t.Fatal("expected error for empty seedID, got nil")
	}
}

func TestGraphExpandResultStructure(t *testing.T) {
	// Validate that GraphExpandResult contains the expected fields.
	r := memory.GraphExpandResult{
		Seed:     &memory.KnowledgeNodeWithLinks{},
		Outgoing: []memory.KnowledgeNode{},
		Incoming: []memory.KnowledgeNode{},
		Linked:   []memory.KnowledgeNode{},
	}
	if r.Seed == nil {
		t.Fatal("GraphExpandResult.Seed should not be nil")
	}
	if r.Outgoing == nil {
		t.Fatal("GraphExpandResult.Outgoing should not be nil")
	}
	if r.Incoming == nil {
		t.Fatal("GraphExpandResult.Incoming should not be nil")
	}
	if r.Linked == nil {
		t.Fatal("GraphExpandResult.Linked should not be nil")
	}
}
