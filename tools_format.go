package jot

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// FormatEntriesForContext formats entries into a readable string for the LLM context.
func FormatEntriesForContext(entries []Entry, maxChars int) string {
	if len(entries) == 0 {
		return "No entries found."
	}

	var lines []string
	totalRunes := 0

	for i, e := range entries {
		ts := e.Timestamp
		if ts == "" {
			ts = "(no date)"
		} else {
			ts = SafeTruncate(ts, 19)
		}
		content := SanitizePrompt(e.Content)
		line := fmt.Sprintf("[%s] (%s) %s", ts, e.Source, content)
		lineRunes := utf8.RuneCountInString(line)
		if totalRunes+lineRunes+1 > maxChars {
			lines = append(lines, fmt.Sprintf("... and %d more entries (truncated)", len(entries)-i))
			break
		}
		lines = append(lines, line)
		totalRunes += lineRunes + 1
	}

	return strings.Join(lines, "\n")
}

// FormatQueriesForContext formats queries into a readable string for the LLM context.
func FormatQueriesForContext(queries []QueryLog, maxChars int) string {
	if len(queries) == 0 {
		return "No queries found."
	}

	var lines []string
	totalRunes := 0

	for i, q := range queries {
		answer := SanitizePrompt(q.Answer)
		if utf8.RuneCountInString(answer) > 300 {
			answer = SafeTruncate(answer, 300) + "..."
		}
		ts := q.Timestamp
		if ts == "" {
			ts = "(no date)"
		} else {
			ts = SafeTruncate(ts, 19)
		}
		question := SanitizePrompt(q.Question)
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

// ToolResult represents the result of executing a tool.
type ToolResult struct {
	Success bool
	Result  string
}
