package agent

import (
	"strings"
	"testing"
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
