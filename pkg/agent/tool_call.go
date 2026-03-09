package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

// StructuredToolCall is the MCP-style payload when using compact tools: model outputs JSON with tool name and args.
type StructuredToolCall struct {
	Tool string                 `json:"tool"`
	Args map[string]interface{} `json:"args"`
}

// ParseStructuredToolCall extracts a single tool call from model output.
// Looks for a fenced JSON block (```json ... ``` or ``` ... ```) containing {"tool": "name", "args": {...}}.
// Returns name, args, true if a valid block was found; otherwise "", nil, false.
func ParseStructuredToolCall(text string) (name string, args map[string]interface{}, found bool) {
	text = strings.TrimSpace(text)
	// Match ```json ... ``` or ``` ... ```
	re := regexp.MustCompile("(?s)```(?:json)?\\s*\\n?\\s*([^`]+)```")
	matches := re.FindStringSubmatch(text)
	if len(matches) < 2 {
		return "", nil, false
	}
	block := strings.TrimSpace(matches[1])
	var payload StructuredToolCall
	if err := json.Unmarshal([]byte(block), &payload); err != nil {
		return "", nil, false
	}
	name = strings.TrimSpace(payload.Tool)
	if name == "" {
		return "", nil, false
	}
	if payload.Args == nil {
		payload.Args = make(map[string]interface{})
	}
	return name, payload.Args, true
}
