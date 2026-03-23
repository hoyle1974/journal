package impl

import "testing"

func TestIsExplicitKnowledgeOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "routine declarative blocked", content: "I recently moved to San Francisco.", want: false},
		{name: "remember command allowed", content: "Please remember that my safe code is 1234.", want: true},
		{name: "correction allowed", content: "Actually, Alice works at Microsoft now.", want: true},
		{name: "store prefix allowed", content: "store: Alice prefers dark chocolate", want: true},
		{name: "non override declarative blocked", content: "I had coffee with Sarah.", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isExplicitKnowledgeOverride(tc.content)
			if got != tc.want {
				t.Fatalf("isExplicitKnowledgeOverride(%q)=%v, want %v", tc.content, got, tc.want)
			}
		})
	}
}
