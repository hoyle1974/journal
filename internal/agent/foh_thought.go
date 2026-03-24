package agent

import (
	"regexp"
	"strings"
	"unicode/utf8"
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

// maxThoughtCharsPerTrace limits stored/API reasoning_trace size per iteration (UTF-8 safe).
const maxThoughtCharsPerTrace = 12000

// truncateThoughtForTrace caps one iteration's thought for ReasoningTrace / JSON responses.
func truncateThoughtForTrace(th string) string {
	th = strings.TrimSpace(th)
	if th == "" {
		return th
	}
	if utf8.RuneCountInString(th) <= maxThoughtCharsPerTrace {
		return th
	}
	runes := []rune(th)
	return strings.TrimSpace(string(runes[:maxThoughtCharsPerTrace])) + "\n… [truncated for trace size]"
}

// thoughtSuggestsKnowledgeGap returns true when the model's thought block explicitly lists
// non-empty "Identified gaps" (CoT-assisted gap detection, phase 5).
func thoughtSuggestsKnowledgeGap(th string) bool {
	th = strings.TrimSpace(th)
	if th == "" {
		return false
	}
	lower := strings.ToLower(th)
	idx := strings.Index(lower, "identified gaps:")
	if idx < 0 {
		return false
	}
	rest := strings.TrimSpace(th[idx+len("Identified gaps:"):])
	if rest == "" {
		return false
	}
	var firstLine string
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*• \t"))
		if line != "" {
			firstLine = line
			break
		}
	}
	if firstLine == "" {
		return false
	}
	fl := strings.ToLower(strings.TrimSpace(firstLine))
	switch fl {
	case "none", "n/a", "na", "nothing", "-", "no", "no gaps", "unknown":
		return false
	}
	return true
}
