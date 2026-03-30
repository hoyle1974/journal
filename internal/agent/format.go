package agent

import (
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/memory"
)

// formatEntriesForPrompt renders log entries as a numbered list for LLM prompts.
func formatEntriesForPrompt(entries []memory.Entry) string {
	if len(entries) == 0 {
		return "(no recent entries)"
	}
	var sb strings.Builder
	for i, e := range entries {
		ts := e.Timestamp
		if len(ts) > 10 {
			ts = ts[:10]
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, ts, e.Content))
	}
	return strings.TrimRight(sb.String(), "\n")
}
