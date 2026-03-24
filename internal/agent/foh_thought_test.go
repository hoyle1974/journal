package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestThoughtSuggestsKnowledgeGap_StillWorks(t *testing.T) {
	if !thoughtSuggestsKnowledgeGap("Identified gaps: missing last week data") {
		t.Fatal("expected gap detected")
	}
	if thoughtSuggestsKnowledgeGap("Identified gaps: none") {
		t.Fatal("expected no gap for 'none'")
	}
}

func TestThoughtSuggestsKnowledgeGap(t *testing.T) {
	tests := []struct {
		name string
		th   string
		want bool
	}{
		{"empty", "", false},
		{"no section", "just thinking", false},
		{"gaps none", "<thought>\nIdentified gaps: none\n</thought>", false},
		{"gaps substantive", "Identified gaps: Q3 revenue figure from journal", true},
		{"gaps multiline ignored", "Identified gaps:\nstill need dates", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := thoughtSuggestsKnowledgeGap(tt.th); got != tt.want {
				t.Errorf("thoughtSuggestsKnowledgeGap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTruncateThoughtForTrace(t *testing.T) {
	short := "hello"
	if truncateThoughtForTrace(short) != short {
		t.Errorf("short text should not change")
	}
	long := strings.Repeat("x", maxThoughtCharsPerTrace+500)
	out := truncateThoughtForTrace(long)
	if !strings.HasSuffix(out, "… [truncated for trace size]") {
		t.Errorf("expected truncation suffix, got len=%d", len(out))
	}
	if utf8.RuneCountInString(out) > maxThoughtCharsPerTrace+40 {
		t.Errorf("truncated output unexpectedly long: %d runes", utf8.RuneCountInString(out))
	}
}
