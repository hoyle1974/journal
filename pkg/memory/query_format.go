package memory

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackstrohm/jot/pkg/utils"
)

// FormatQueriesForContext formats queries into a readable string for LLM context.
// QueryLog is defined in query_nodes.go (Task 5).
func FormatQueriesForContext(queries []QueryLog, maxChars int) string {
	if len(queries) == 0 {
		return "No queries found."
	}
	var lines []string
	totalRunes := 0
	for i, q := range queries {
		answer := utils.SanitizePrompt(q.Answer)
		if utf8.RuneCountInString(answer) > 300 {
			answer = utils.TruncateString(answer, 300) + "..."
		}
		ts := q.Timestamp
		if ts == "" {
			ts = "(no date)"
		} else {
			ts = utils.TruncateString(ts, 19)
		}
		question := utils.SanitizePrompt(q.Question)
		line := fmt.Sprintf("[%s] (%s)\n  Q: %s\n  A: %s", ts, q.Source, question, answer)
		lineRunes := utf8.RuneCountInString(line)
		if totalRunes+lineRunes+2 > maxChars {
			lines = append(lines, fmt.Sprintf("... and %d more queries (truncated)", len(queries)-i))
			break
		}
		lines = append(lines, line)
		totalRunes += lineRunes + 2
	}
	return strings.Join(lines, "\n\n")
}
