package memory

import (
	"testing"
)

func TestParseSPOTriple(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         string
		wantNil       bool
		wantSubject   string
		wantPredicate string
		wantObject    string
	}{
		{
			name:          "valid triple",
			input:         "Sarah | prefers | Oat Milk",
			wantNil:       false,
			wantSubject:   "Sarah",
			wantPredicate: "prefers",
			wantObject:    "Oat Milk",
		},
		{
			name:    "flat fact, no pipes",
			input:   "Sarah's birthday is March 5",
			wantNil: true,
		},
		{
			name:    "one pipe only",
			input:   "a | b",
			wantNil: true,
		},
		{
			// SplitN with n=3 means a fourth "|" ends up inside the Object field.
			// The implementation does NOT reject this — the object becomes "c | d".
			// This test documents the actual behaviour.
			name:          "four segments: extra pipe absorbed into Object",
			input:         "a | b | c | d",
			wantNil:       false,
			wantSubject:   "a",
			wantPredicate: "b",
			wantObject:    "c | d",
		},
		{
			name:          "extra whitespace is trimmed",
			input:         "  Alice  |  works at  |  ACME Corp  ",
			wantNil:       false,
			wantSubject:   "Alice",
			wantPredicate: "works at",
			wantObject:    "ACME Corp",
		},
		{
			name:    "empty subject",
			input:   " | prefers | Oat Milk",
			wantNil: true,
		},
		{
			name:    "empty predicate",
			input:   "Sarah |  | Oat Milk",
			wantNil: true,
		},
		{
			name:    "empty object",
			input:   "Sarah | prefers | ",
			wantNil: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseSPOTriple(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("ParseSPOTriple(%q) = %+v, want nil", tc.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ParseSPOTriple(%q) = nil, want non-nil triple", tc.input)
			}
			if got.Subject != tc.wantSubject {
				t.Errorf("Subject: got %q, want %q", got.Subject, tc.wantSubject)
			}
			if got.Predicate != tc.wantPredicate {
				t.Errorf("Predicate: got %q, want %q", got.Predicate, tc.wantPredicate)
			}
			if got.Object != tc.wantObject {
				t.Errorf("Object: got %q, want %q", got.Object, tc.wantObject)
			}
		})
	}
}

func TestNormalizedPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"Is Part Of", "is_part_of"},
		{"works-at", "works_at"},
		{"prefers", "prefers"},
		{"Works At", "works_at"},
		{"LOVES", "loves"},
		{"has-sibling", "has_sibling"},
		{"  leading space  ", "leading_space"},
		{"mixed-spaces and-hyphens", "mixed_spaces_and_hyphens"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := NormalizedPredicate(tc.input)
			if got != tc.want {
				t.Errorf("NormalizedPredicate(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestIsSPOTriple(t *testing.T) {
	t.Parallel()

	if !IsSPOTriple("Sarah | prefers | Oat Milk") {
		t.Error("IsSPOTriple: expected true for valid triple")
	}
	if IsSPOTriple("Sarah's birthday is March 5") {
		t.Error("IsSPOTriple: expected false for flat fact")
	}
}
