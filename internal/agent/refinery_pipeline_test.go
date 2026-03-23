package agent

import "testing"

func TestParseRefineryTriples(t *testing.T) {
	triples := parseRefineryTriples([]string{
		"Gloria | works_at | Anthropic",
		"bad line",
		"Ada | prefers | tea",
	})
	if len(triples) != 3 {
		t.Fatalf("expected 3 triples (including reject entry), got %d", len(triples))
	}
	if triples[0].Predicate != "works_at" {
		t.Fatalf("expected raw predicate works_at, got %q", triples[0].Predicate)
	}
	if triples[1].ParseErr == "" {
		t.Fatalf("expected parse error for malformed line")
	}
	if triples[1].RawLine != "bad line" {
		t.Fatalf("expected raw line to be preserved, got %q", triples[1].RawLine)
	}
}
