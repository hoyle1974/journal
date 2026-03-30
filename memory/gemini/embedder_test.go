package gemini

import (
	"context"
	"testing"

	"github.com/jackstrohm/jot/memory"
)

// compile-time assertion: embedder must satisfy the full Embedder interface including EmbedContent.
var _ memory.Embedder = (*embedder)(nil)

func TestEmbedder_EmbedContent_NilClient(t *testing.T) {
	e := &embedder{client: nil}
	_, err := e.EmbedContent(context.Background(), []memory.EmbedPart{{Text: "hello"}})
	if err == nil {
		t.Fatal("expected error with nil client, got nil")
	}
}

func TestEmbedder_EmbedContent_EmptyParts(t *testing.T) {
	e := &embedder{client: nil}
	_, err := e.EmbedContent(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty parts, got nil")
	}
}

func TestEmbedder_GenerateEmbedding_NilClient(t *testing.T) {
	e := &embedder{client: nil}
	_, err := e.GenerateEmbedding(context.Background(), "hello", memory.EmbedTaskRetrievalQuery)
	if err == nil {
		t.Fatal("expected error with nil client, got nil")
	}
}
