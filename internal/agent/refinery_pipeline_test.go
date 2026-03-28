package agent

import (
	"context"
	"testing"
)

func TestRunRefineryPipelineReturnsNodeIDs(t *testing.T) {
	// nil app returns error, not panic, and empty IDs
	ids, err := runRefineryPipeline(context.Background(), nil, "uuid-1", "test content", "")
	if err == nil {
		t.Fatal("expected error for nil app")
	}
	if ids != nil {
		t.Fatalf("expected nil ids on error, got %v", ids)
	}
}

func TestParseRefineryTriples(t *testing.T) {
	triples := parseRefineryTriples([]string{
		"Gloria | works_at | Anthropic | person | project",
		"bad line",
		"Ada | moved_to | Paris",
	}, "")
	if len(triples) != 3 {
		t.Fatalf("expected 3 triples (including reject entry), got %d", len(triples))
	}
	if triples[0].Predicate != "works_at" {
		t.Fatalf("expected raw predicate works_at, got %q", triples[0].Predicate)
	}
	if triples[0].SubType != "person" || triples[0].ObjType != "project" {
		t.Fatalf("expected typed triple, got sub=%q obj=%q", triples[0].SubType, triples[0].ObjType)
	}
	if triples[1].ParseErr == "" {
		t.Fatalf("expected parse error for malformed line")
	}
	if triples[1].RawLine != "bad line" {
		t.Fatalf("expected raw line to be preserved, got %q", triples[1].RawLine)
	}
	if triples[2].SubType != "generic" || triples[2].ObjType != "generic" {
		t.Fatalf("expected default generic types for 3-field triples, got sub=%q obj=%q", triples[2].SubType, triples[2].ObjType)
	}
}
