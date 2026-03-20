package journal

import (
	"strings"
	"testing"
)

func TestTruncateTimestamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ts     string
		maxLen int
		want   string
	}{
		{"date only", "2024-01-15T10:00:00Z", DateDisplayLen, "2024-01-15"},
		{"datetime", "2024-01-15T10:00:00Z", DateTimeDisplayLen, "2024-01-15T10:00:00"},
		{"short stays intact", "2024-01-15", DateDisplayLen, "2024-01-15"},
		{"empty string", "", DateDisplayLen, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := TruncateTimestamp(tc.ts, tc.maxLen)
			if got != tc.want {
				t.Errorf("TruncateTimestamp(%q, %d) = %q, want %q", tc.ts, tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestFormatQueriesForContext(t *testing.T) {
	t.Parallel()

	// nil and empty slice both return the sentinel message.
	for _, tc := range []struct {
		name    string
		queries []QueryLog
	}{
		{"nil slice", nil},
		{"empty slice", []QueryLog{}},
	} {
		t.Run(tc.name+" returns no queries message", func(t *testing.T) {
			t.Parallel()
			got := FormatQueriesForContext(tc.queries, 1000)
			if got != noQueriesFound {
				t.Errorf("got %q, want %q", got, noQueriesFound)
			}
		})
	}

	t.Run("single query contains all fields", func(t *testing.T) {
		t.Parallel()
		ql := []QueryLog{{
			Question:  "What is Go?",
			Answer:    "A compiled language.",
			Source:    "cli",
			Timestamp: "2024-01-15T10:00:00Z",
		}}
		got := FormatQueriesForContext(ql, 10000)
		for _, want := range []string{"What is Go?", "A compiled language.", "cli", "2024-01-15T10:00:00"} {
			if !strings.Contains(got, want) {
				t.Errorf("output missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("empty timestamp shows no date", func(t *testing.T) {
		t.Parallel()
		ql := []QueryLog{{Question: "Q?", Answer: "A.", Source: "test"}}
		got := FormatQueriesForContext(ql, 1000)
		if !strings.Contains(got, "(no date)") {
			t.Errorf("expected (no date) in output: %q", got)
		}
	})

	t.Run("timestamp truncated to 19 chars", func(t *testing.T) {
		t.Parallel()
		// "2024-01-15T10:00:00Z" is 21 chars; should be truncated to 19.
		ql := []QueryLog{{Question: "Q?", Answer: "A.", Source: "t", Timestamp: "2024-01-15T10:00:00Z"}}
		got := FormatQueriesForContext(ql, 10000)
		if strings.Contains(got, "2024-01-15T10:00:00Z") {
			t.Errorf("timestamp not truncated; found full 21-char stamp in: %q", got)
		}
		if !strings.Contains(got, "2024-01-15T10:00:00") {
			t.Errorf("expected 19-char timestamp in: %q", got)
		}
	})

	t.Run("answer longer than 300 runes is truncated with ellipsis", func(t *testing.T) {
		t.Parallel()
		longAnswer := strings.Repeat("x", 350)
		ql := []QueryLog{{Question: "Q?", Answer: longAnswer, Source: "test"}}
		got := FormatQueriesForContext(ql, 100000)
		if !strings.Contains(got, "...") {
			t.Errorf("expected ellipsis for long answer, got: %q", got)
		}
		if strings.Count(got, "x") != 300 {
			t.Errorf("expected exactly 300 x's in truncated answer, got %d", strings.Count(got, "x"))
		}
	})

	t.Run("answer of exactly 300 runes is not truncated", func(t *testing.T) {
		t.Parallel()
		answer := strings.Repeat("y", 300)
		ql := []QueryLog{{Question: "Q?", Answer: answer, Source: "test"}}
		got := FormatQueriesForContext(ql, 100000)
		if strings.Count(got, "y") != 300 {
			t.Errorf("300-rune answer should not be truncated, got %d y's", strings.Count(got, "y"))
		}
	})

	t.Run("maxChars exceeded adds truncation message", func(t *testing.T) {
		t.Parallel()
		queries := []QueryLog{
			{Question: "First question?", Answer: "First answer.", Source: "test"},
			{Question: "Second question?", Answer: "Second answer.", Source: "test"},
		}
		// maxChars=5 forces truncation before any query fits.
		got := FormatQueriesForContext(queries, 5)
		if !strings.Contains(got, "more queries (truncated)") {
			t.Errorf("expected truncation message, got: %q", got)
		}
	})

	t.Run("multiple queries within budget separated by double newline", func(t *testing.T) {
		t.Parallel()
		queries := []QueryLog{
			{Question: "Q1?", Answer: "A1.", Source: "s1"},
			{Question: "Q2?", Answer: "A2.", Source: "s2"},
		}
		got := FormatQueriesForContext(queries, 100000)
		if !strings.Contains(got, "\n\n") {
			t.Errorf("expected double newline between queries: %q", got)
		}
		for _, want := range []string{"Q1?", "A1.", "Q2?", "A2."} {
			if !strings.Contains(got, want) {
				t.Errorf("output missing %q: %q", want, got)
			}
		}
	})
}
