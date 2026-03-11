package utils

import (
	"strings"
)

// ParseKeyValueMap parses key/value text (no JSON). It returns:
// - simple: key -> single value for lines like "key: value". Keys are lowercased.
// - sections: section name -> list of lines for block sections like "entities:" followed by one line per item. Keys are lowercased.
// Section headers are lines ending with ":" and no value (e.g. "entities:" or "open_loops:").
// Subsequent non-empty lines are collected until the next "key:" or "section:" line.
func ParseKeyValueMap(text string) (simple map[string]string, sections map[string][]string) {
	simple = make(map[string]string)
	sections = make(map[string][]string)
	lines := strings.Split(text, "\n")
	var currentSection string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx >= 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			keyLower := strings.ToLower(key)
			// Section header: "key:" with no value
			if value == "" {
				currentSection = keyLower
				continue
			}
			currentSection = ""
			simple[keyLower] = value
			continue
		}
		if currentSection != "" {
			sections[currentSection] = append(sections[currentSection], line)
		}
	}
	return simple, sections
}
