package memory_test

import (
	"context"
	"testing"

	"cloud.google.com/go/firestore"
	"google.golang.org/genai"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/memory"
)

// stubEnv satisfies infra.ToolEnv with nil returns (no real Firestore).
type stubEnv struct{}

func (s *stubEnv) Config() *config.Config { return &config.Config{} }
func (s *stubEnv) Firestore(_ context.Context) (*firestore.Client, error) {
	return nil, nil
}
func (s *stubEnv) Dispatch(_ context.Context, _ *infra.LLMRequest) (*genai.GenerateContentResponse, error) {
	return nil, nil
}

func TestGraphExpandValidation(t *testing.T) {
	ctx := context.Background()

	// nil env should return an error
	_, err := memory.GraphExpand(ctx, nil, "some-uuid", 1, 10)
	if err == nil {
		t.Fatal("expected error for nil env, got nil")
	}

	// empty seedID should return an error
	_, err = memory.GraphExpand(ctx, &stubEnv{}, "", 1, 10)
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
