package agent

import (
	"strings"
)

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
