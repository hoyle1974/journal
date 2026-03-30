package memory

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackstrohm/jot/pkg/utils"
)

const (
	DateDisplayLen     = 10
	DateTimeDisplayLen = 19

	noEntriesFound = "No entries found."
	noQueriesFound = "No queries found."
)

// TruncateTimestamp truncates ts for display (date 10 or datetime 19 runes).
func TruncateTimestamp(ts string, maxLen int) string {
	return utils.TruncateString(ts, maxLen)
}

// FormatEntriesForContext formats entries into a readable string for LLM context.
func FormatEntriesForContext(entries []Entry, maxChars int) string {
	if len(entries) == 0 {
		return noEntriesFound
	}
	var lines []string
	totalRunes := 0
	for i, e := range entries {
		ts := e.Timestamp
		if ts == "" {
			ts = "(no date)"
		} else {
			ts = utils.TruncateString(ts, 19)
		}
		content := utils.SanitizePrompt(e.Content)
		line := fmt.Sprintf("[%s] (%s) %s", ts, e.Source, content)
		if e.ImageURL != "" {
			if e.ParsedImageDescription != "" {
				desc := utils.SanitizePrompt(e.ParsedImageDescription)
				line += fmt.Sprintf("\n[Attached Image Content: %s]", desc)
			} else {
				line += "\n[Attached image]"
			}
			if e.UUID != "" {
				line += fmt.Sprintf("\n[Entry UUID: %s]", e.UUID)
			}
		}
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
