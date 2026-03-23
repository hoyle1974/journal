package memory

import (
	"context"
	"testing"
)

func TestAddEntry_EmptyContent(t *testing.T) {
	s := New(nil, nil, nil)
	_, err := s.AddEntry(context.Background(), "", "test", nil, "")
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
}

func TestFormatEntriesForContext_Empty(t *testing.T) {
	result := FormatEntriesForContext(nil, 1000)
	if result != "No entries found." {
		t.Errorf("expected 'No entries found.', got %q", result)
	}
}

func TestFormatQueriesForContext_Empty(t *testing.T) {
	result := FormatQueriesForContext(nil, 1000)
	if result != "No queries found." {
		t.Errorf("expected 'No queries found.', got %q", result)
	}
}
