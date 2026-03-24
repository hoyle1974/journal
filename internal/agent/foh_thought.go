package agent

import (
	"strings"
	"unicode/utf8"
)

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

// thoughtSuggestsKnowledgeGap returns true when the model's thinking block explicitly lists
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
