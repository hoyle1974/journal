package memory

import (
	"context"
	"testing"
	"github.com/jackstrohm/jot/internal/infra"
)

func init() {
	// Initialize observability for all tests in this package.
	infra.InitObservability(nil)
}

func TestAnalyzeJournalEntry_ShortContent(t *testing.T) {
	// Content shorter than 20 chars should return nil, nil (no analysis)
	shortContent := "short"
	result, err := AnalyzeJournalEntry(context.Background(), nil, shortContent, "uuid1", "2026-03-19")
	if result != nil || err != nil {
		t.Errorf("short content: expected (nil, nil), got (%v, %v)", result, err)
	}
}

func TestAnalyzeJournalEntry_NilEnv(t *testing.T) {
	longContent := "this is a longer journal entry that exceeds the 20 char minimum for analysis"
	_, err := AnalyzeJournalEntry(context.Background(), nil, longContent, "uuid1", "2026-03-19")
	if err == nil {
		t.Fatal("expected error for nil env with long content, got nil")
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
