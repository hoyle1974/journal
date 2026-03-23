package agent

import "testing"

func TestParseRefineryTriples(t *testing.T) {
	triples := parseRefineryTriples([]string{
		"Gloria | works_at | Anthropic",
		"bad line",
		"Ada | prefers | tea",
	})
	if len(triples) != 2 {
		t.Fatalf("expected 2 triples, got %d", len(triples))
	}
	if triples[0].Predicate != "works_at" {
		t.Fatalf("expected normalized predicate works_at, got %q", triples[0].Predicate)
	}
}
