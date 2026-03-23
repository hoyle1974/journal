package memory

import "testing"

func TestLooksLikeEntityDocID(t *testing.T) {
	if !looksLikeEntityDocID("entity_abc123") {
		t.Fatalf("expected entity id to be detected")
	}
	if looksLikeEntityDocID("Jack") {
		t.Fatalf("expected plain name to not be treated as entity id")
	}
}

func TestRelationshipContent(t *testing.T) {
	got := relationshipContent("Jack", "works_at", "Anthropic", "entity_a", "entity_b")
	if got != "Jack works_at Anthropic" {
		t.Fatalf("unexpected relationship content: %q", got)
	}
	fallback := relationshipContent("", "works_at", "", "entity_a", "entity_b")
	if fallback != "entity_a works_at entity_b" {
		t.Fatalf("unexpected fallback relationship content: %q", fallback)
	}
}
