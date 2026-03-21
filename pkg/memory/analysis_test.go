package memory

import (
	"context"
	"testing"
)

func TestAnalyzeJournalEntry_ShortContent(t *testing.T) {
	// Content shorter than 20 chars should return nil, nil (no analysis)
	s := New(nil, nil, nil)
	result, err := s.AnalyzeJournalEntry(context.Background(), "short", "uuid1", "2026-03-19")
	if result != nil || err != nil {
		t.Errorf("short content: expected (nil, nil), got (%v, %v)", result, err)
	}
}

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
