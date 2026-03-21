package agent

import (
	"strings"
	"testing"

	"github.com/hoyle1974/memory"
)

// TestMergeDreamerFactsSPOFields verifies that the SPO enrichment logic inside
// mergeDreamerFacts — which calls memory.ParseSPOTriple and
// memory.NormalizedPredicate — correctly populates Predicate and ObjectValue on
// a mergedFact.  mergeDreamerFacts itself cannot be called in a unit test
// because it requires a live embedding API and Firestore; instead, we replicate
// the exact three-line enrichment block and confirm it behaves correctly across
// a variety of fact strings.
func TestMergeDreamerFactsSPOFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		factContent     string
		wantPredicate   string
		wantObjectValue string
	}{
		{
			name:            "valid SPO triple sets predicate and object",
			factContent:     "Sarah | prefers | Oat Milk",
			wantPredicate:   "prefers",
			wantObjectValue: "Oat Milk",
		},
		{
			name:            "predicate with spaces is normalised",
			factContent:     "Alice | Is Part Of | Engineering",
			wantPredicate:   "is_part_of",
			wantObjectValue: "Engineering",
		},
		{
			name:            "predicate with hyphens is normalised",
			factContent:     "Bob | works-at | ACME Corp",
			wantPredicate:   "works_at",
			wantObjectValue: "ACME Corp",
		},
		{
			name:            "flat fact leaves predicate and object empty",
			factContent:     "Sarah's birthday is March 5",
			wantPredicate:   "",
			wantObjectValue: "",
		},
		{
			name:            "one pipe is not a triple",
			factContent:     "foo | bar",
			wantPredicate:   "",
			wantObjectValue: "",
		},
		{
			// SplitN with n=3 absorbs extra "|" into the Object field rather than
			// rejecting the line. Document the actual enrichment behaviour.
			name:            "four segments: extra pipe absorbed into Object",
			factContent:     "a | b | c | d",
			wantPredicate:   "b",
			wantObjectValue: "c | d",
		},
		{
			name:            "object value whitespace is trimmed",
			factContent:     "Carol | loves |   Jazz Music   ",
			wantPredicate:   "loves",
			wantObjectValue: "Jazz Music",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Replicate the exact enrichment block from mergeDreamerFacts.
			mf := mergedFact{
				Content: tc.factContent,
			}
			if triple := memory.ParseSPOTriple(tc.factContent); triple != nil {
				mf.Predicate = memory.NormalizedPredicate(triple.Predicate)
				mf.ObjectValue = strings.TrimSpace(triple.Object)
			}

			if mf.Predicate != tc.wantPredicate {
				t.Errorf("Predicate: got %q, want %q", mf.Predicate, tc.wantPredicate)
			}
			if mf.ObjectValue != tc.wantObjectValue {
				t.Errorf("ObjectValue: got %q, want %q", mf.ObjectValue, tc.wantObjectValue)
			}
		})
	}
}
