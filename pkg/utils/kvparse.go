package utils

import (
	"strings"
)

// isIdentifierKey returns true if s looks like a machine-generated key:
// only ASCII letters, digits, and underscores (no spaces). This lets us
// reject chatty LLM preamble lines like "Here are your results:" before
// they get misinterpreted as section headers or key/value pairs.
func isIdentifierKey(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// ParseKeyValueMap parses key/value text (no JSON). It returns:
// - simple: key -> single value for lines like "key: value". Keys are lowercased.
// - sections: section name -> list of lines for block sections like "entities:" followed by one line per item. Keys are lowercased.
// Section headers are lines ending with ":" and no value (e.g. "entities:" or "open_loops:").
// Subsequent non-empty lines are collected until the next "key:" or "section:" line.
//
// Lines whose text before ":" contains spaces (e.g. LLM preamble like "Here are the results:")
// are silently skipped so chatty model output does not corrupt the parsed data.
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
		// Check whether this line is a valid key/value or section header.
		// A valid key must be identifier-shaped (letters, digits, underscores only)
		// to prevent chatty LLM preamble (e.g. "Here are your results:") from being
		// mistaken for a section header or key/value pair.
		idx := strings.Index(line, ":")
		if idx >= 0 {
			key := strings.TrimSpace(line[:idx])
			if isIdentifierKey(key) {
				value := strings.TrimSpace(line[idx+1:])
				keyLower := strings.ToLower(key)
				// Valid key always resets any active section.
				currentSection = ""
				if value == "" {
					// Section header: "key:" with no value.
					currentSection = keyLower
				} else {
					simple[keyLower] = value
				}
				continue
			}
		}
		// Not a valid key line. If we're inside a section, collect the line raw
		// (section content may freely contain ":", "|", spaces, etc.).
		if currentSection != "" {
			sections[currentSection] = append(sections[currentSection], line)
		}
		// Outside a section with no valid key: preamble/noise — skip.
	}
	return simple, sections
}
