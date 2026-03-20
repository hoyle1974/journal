package agent

import (
	"testing"
)

func TestParseSPOLines_Valid(t *testing.T) {
	input := "Gloria | is_wife_of | Jeff\nGideon | is_child_of | Gloria\n"
	triples := parseSPOLines(input)
	if len(triples) != 2 {
		t.Fatalf("expected 2 triples, got %d", len(triples))
	}
	if triples[0].Subject != "Gloria" {
		t.Errorf("expected Subject=Gloria, got %q", triples[0].Subject)
	}
	if triples[0].Predicate != "is_wife_of" {
		t.Errorf("expected Predicate=is_wife_of, got %q", triples[0].Predicate)
	}
	if triples[0].Object != "Jeff" {
		t.Errorf("expected Object=Jeff, got %q", triples[0].Object)
	}
}

func TestParseSPOLines_None(t *testing.T) {
	triples := parseSPOLines("NONE")
	if len(triples) != 0 {
		t.Fatalf("expected 0 triples for NONE output, got %d", len(triples))
	}
}

func TestParseSPOLines_Empty(t *testing.T) {
	triples := parseSPOLines("")
	if len(triples) != 0 {
		t.Fatalf("expected 0 triples for empty input, got %d", len(triples))
	}
}

func TestParseSPOLines_Malformed(t *testing.T) {
	// Lines without exactly two "|" separators must be skipped.
	input := "Gloria is Jeff's wife\nGideon | is_child_of | Gloria\n"
	triples := parseSPOLines(input)
	if len(triples) != 1 {
		t.Fatalf("expected 1 valid triple (malformed line skipped), got %d", len(triples))
	}
	if triples[0].Subject != "Gideon" {
		t.Errorf("expected Subject=Gideon, got %q", triples[0].Subject)
	}
}

func TestParseSPOLines_PredicateNormalized(t *testing.T) {
	// Raw predicate "works at" should be normalized to "works_at".
	input := "Gloria | works at | Anthropic\n"
	triples := parseSPOLines(input)
	if len(triples) != 1 {
		t.Fatalf("expected 1 triple, got %d", len(triples))
	}
	if triples[0].Predicate != "works_at" {
		t.Errorf("expected normalized predicate works_at, got %q", triples[0].Predicate)
	}
}
