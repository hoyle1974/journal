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

func TestStableRelID_Deterministic(t *testing.T) {
	id1 := stableRelID("subj1", "works_at", "obj1")
	id2 := stableRelID("subj1", "works_at", "obj1")
	if id1 != id2 {
		t.Fatalf("stableRelID must be deterministic: got %q and %q", id1, id2)
	}
	id3 := stableRelID("subj1", "works_at", "obj2")
	if id1 == id3 {
		t.Fatalf("different triples must produce different IDs")
	}
}

func TestRelationshipTimestampUpdateSlice(t *testing.T) {
	ts := "2026-03-28T10:00:00Z"
	updates := buildRelationshipReobserveUpdates("entry-uuid-1", ts)
	hasTimestamp := false
	hasEntryIDs := false
	for _, u := range updates {
		if u.Path == "timestamp" && u.Value == ts {
			hasTimestamp = true
		}
		if u.Path == "journal_entry_ids" {
			hasEntryIDs = true
			if u.Value == nil {
				t.Error("journal_entry_ids update value must not be nil")
			}
		}
	}
	if !hasTimestamp {
		t.Error("expected timestamp update when ts is non-empty")
	}
	if !hasEntryIDs {
		t.Error("expected journal_entry_ids ArrayUnion update")
	}
}

func TestRelationshipTimestampUpdateSlice_EmptyTS(t *testing.T) {
	updates := buildRelationshipReobserveUpdates("entry-uuid-1", "")
	for _, u := range updates {
		if u.Path == "timestamp" {
			t.Error("should not update timestamp when ts is empty")
		}
	}
	if len(updates) != 1 {
		t.Errorf("expected exactly 1 update (journal_entry_ids), got %d", len(updates))
	}
}
