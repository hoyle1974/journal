package agent

import (
	"regexp"
	"strings"
)

// thoughtBlockRegex extracts optional <thought>...</thought> blocks from model text.
// This is an exception to the usual K/V-only parsing for LLM output; FOH still uses
// ParseKeyValueMap / structured tool calls for tools and final answers after stripping.
// Tool calls remain K/V or native function calls; CoT is orthogonal (brief: FOH CoT).
var thoughtBlockRegex = regexp.MustCompile(`(?s)<thought>(.*?)</thought>`)

// extractThoughtsAndStrip removes all <thought>...</thought> regions and returns the
// concatenated inner text (non-empty blocks joined by "\n---\n") plus the remainder
// suitable for ParseStructuredToolCall / extractMissingInfoAndAnswer.
func extractThoughtsAndStrip(raw string) (thought string, stripped string) {
	raw = strings.TrimSpace(raw)
	matches := thoughtBlockRegex.FindAllStringSubmatch(raw, -1)
	var parts []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		t := strings.TrimSpace(m[1])
		if t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) > 0 {
		thought = strings.Join(parts, "\n---\n")
	}
	stripped = strings.TrimSpace(thoughtBlockRegex.ReplaceAllString(raw, ""))
	return thought, stripped
}
