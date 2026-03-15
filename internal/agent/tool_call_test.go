package agent

import (
	"reflect"
	"testing"
)

func TestParseStructuredToolCall_KV(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantName  string
		wantArgs  map[string]interface{}
		wantFound bool
	}{
		{
			name: "TOOL and ARGS section",
			text: `TOOL: create_task
ARGS:
action | Pick up milk
due_date | 2026-03-15`,
			wantName:  "create_task",
			wantArgs:  map[string]interface{}{"action": "Pick up milk", "due_date": "2026-03-15"},
			wantFound: true,
		},
		{
			name: "single arg",
			text: `TOOL: semantic_search
ARGS:
query | What did I do last week?`,
			wantName:  "semantic_search",
			wantArgs:  map[string]interface{}{"query": "What did I do last week?"},
			wantFound: true,
		},
		{
			name: "tool only, no args",
			text: `TOOL: list_knowledge
ARGS:
`,
			wantName:  "list_knowledge",
			wantArgs:  map[string]interface{}{},
			wantFound: true,
		},
		{
			name:      "empty text",
			text:      "",
			wantFound: false,
		},
		{
			name: "no TOOL key",
			text: `ARGS:
foo | bar`,
			wantFound: false,
		},
		{
			name: "value with pipe",
			text: `TOOL: upsert_knowledge
ARGS:
content | Note: use a | b for options`,
			wantName:  "upsert_knowledge",
			wantArgs:  map[string]interface{}{"content": "Note: use a | b for options"},
			wantFound: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotArgs, gotFound := ParseStructuredToolCall(tt.text)
			if gotFound != tt.wantFound {
				t.Errorf("ParseStructuredToolCall() found = %v, want %v", gotFound, tt.wantFound)
				return
			}
			if !tt.wantFound {
				return
			}
			if gotName != tt.wantName {
				t.Errorf("ParseStructuredToolCall() name = %q, want %q", gotName, tt.wantName)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Errorf("ParseStructuredToolCall() args = %v, want %v", gotArgs, tt.wantArgs)
			}
		})
	}
}
