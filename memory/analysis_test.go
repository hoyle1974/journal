package memory

import "testing"

func TestNormalizeEntityStatus_Values(t *testing.T) {
	cases := []struct{ in, want string }{
		{"in-progress", EntityStatusInProgress},
		{"In Progress", EntityStatusInProgress},
		{"resolved", EntityStatusCompleted},
		{"planned", EntityStatusPlanned},
		{"stalled", EntityStatusStalled},
		{"blocked", EntityStatusStalled},
		{"", EntityStatusInProgress},
		{"unknown", "unknown"},
	}
	for _, c := range cases {
		if got := NormalizeEntityStatus(c.in); got != c.want {
			t.Errorf("NormalizeEntityStatus(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
