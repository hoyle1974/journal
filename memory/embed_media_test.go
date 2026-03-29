package memory

import (
	"context"
	"testing"
)

func TestStore_EmbedEntryMedia_NilStore(t *testing.T) {
	// compile-time check: EmbedEntryMedia must exist on *Store
	var s *Store
	_ = s.EmbedEntryMedia
}

func TestEmbedEntryMedia_EmptyBytes(t *testing.T) {
	// Calling with empty bytes should return an error without panicking.
	s := &Store{} // embedder is nil
	err := s.EmbedEntryMedia(context.Background(), "uuid-1", nil, "image/jpeg")
	if err == nil {
		t.Fatal("expected error for empty bytes, got nil")
	}
}
