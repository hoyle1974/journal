package agent

import (
	"testing"
)

func TestExtractUUIDsFromSearchResult_Valid(t *testing.T) {
	input := "1. [person] [2026-01-01] Jeff\n   UUID: abc-123\n\n2. [place] [2026-01-01] The Park\n   UUID: def-456\n"
	got := extractUUIDsFromSearchResult(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 UUIDs, got %d: %v", len(got), got)
	}
	if got[0] != "abc-123" {
		t.Errorf("expected abc-123, got %q", got[0])
	}
	if got[1] != "def-456" {
		t.Errorf("expected def-456, got %q", got[1])
	}
}

func TestExtractUUIDsFromSearchResult_NoUUIDs(t *testing.T) {
	input := "1. [person] [2026-01-01] Jeff — no UUID line here"
	got := extractUUIDsFromSearchResult(input)
	if len(got) != 0 {
		t.Fatalf("expected 0 UUIDs, got %d: %v", len(got), got)
	}
}

func TestExtractUUIDsFromSearchResult_Empty(t *testing.T) {
	got := extractUUIDsFromSearchResult("")
	if len(got) != 0 {
		t.Fatalf("expected 0 UUIDs for empty input, got %d", len(got))
	}
}

func TestExtractUUIDsFromSearchResult_Dedup(t *testing.T) {
	input := "   UUID: abc-123\n   UUID: abc-123\n"
	got := extractUUIDsFromSearchResult(input)
	if len(got) != 1 {
		t.Fatalf("expected 1 unique UUID, got %d: %v", len(got), got)
	}
}
