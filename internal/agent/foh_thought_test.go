package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExtractThoughtsAndStrip(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantThought  string
		wantStripped string
	}{
		{
			name:         "no tags",
			raw:          "Hello world",
			wantThought:  "",
			wantStripped: "Hello world",
		},
		{
			name: "thought and remainder",
			raw: `<thought>
Current status: none
Next step: search
</thought>
TOOL: semantic_search`,
			wantThought: `Current status: none
Next step: search`,
			wantStripped: "TOOL: semantic_search",
		},
		{
			name:         "only thought",
			raw:          "<thought>inner</thought>",
			wantThought:  "inner",
			wantStripped: "",
		},
		{
			name:         "two blocks joined",
			raw:          "<thought>a</thought>\n<thought>b</thought>\nfinal",
			wantThought:  "a\n---\nb",
			wantStripped: "final",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotThought, gotStripped := extractThoughtsAndStrip(tt.raw)
			if gotThought != tt.wantThought {
				t.Errorf("thought = %q, want %q", gotThought, tt.wantThought)
			}
			if strings.TrimSpace(gotStripped) != strings.TrimSpace(tt.wantStripped) {
				t.Errorf("stripped = %q, want %q", gotStripped, tt.wantStripped)
			}
		})
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
