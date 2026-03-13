package agent

import (
	"strings"

	"github.com/jackstrohm/jot/pkg/utils"
)

// ParseStructuredToolCall extracts a single tool call from model output.
// Expects K/V format: TOOL: name, then ARGS: section with "param_name | value" lines.
// Returns name, args, true if a valid block was found; otherwise "", nil, false.
func ParseStructuredToolCall(text string) (name string, args map[string]interface{}, found bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil, false
	}
	simple, sections := utils.ParseKeyValueMap(text)
	name = strings.TrimSpace(simple["tool"])
	if name == "" {
		return "", nil, false
	}
	args = make(map[string]interface{})
	for _, line := range sections["args"] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		k := strings.TrimSpace(parts[0])
		if k == "" {
			continue
		}
		v := ""
		if len(parts) >= 2 {
			v = strings.TrimSpace(parts[1])
		}
		args[k] = v
	}
	return name, args, true
}
