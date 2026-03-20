package memory

import (
	"context"
	"testing"
)

func TestAddEntry_NilEnv(t *testing.T) {
	_, err := AddEntry(context.Background(), nil, "hello", "test", nil, "")
	if err == nil {
		t.Fatal("expected error for nil env, got nil")
	}
}

func TestAddEntry_EmptyContent(t *testing.T) {
	_, err := AddEntry(context.Background(), nil, "", "test", nil, "")
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
}

func TestGetEntries_NilEnv(t *testing.T) {
	_, err := GetEntries(context.Background(), nil, 10)
	if err == nil {
		t.Fatal("expected error for nil env, got nil")
	}
}

func TestQuerySimilarEntries_NilEnv(t *testing.T) {
	_, err := QuerySimilarEntries(context.Background(), nil, []float32{0.1, 0.2}, 5)
	if err == nil {
		t.Fatal("expected error for nil env, got nil")
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
