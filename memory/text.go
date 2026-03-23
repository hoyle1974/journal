package memory

import (
	"strings"
	"unicode/utf8"
)

// truncateString truncates s to at most maxRunes runes. Does NOT append "...".
// Copied from pkg/utils.TruncateString.
func truncateString(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

// truncateToMaxBytes truncates s to at most maxBytes UTF-8 bytes. Does NOT append "...".
// Copied from pkg/utils.TruncateToMaxBytes.
func truncateToMaxBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	runes := []rune(s)
	n := 0
	for i, r := range runes {
		n += utf8.RuneLen(r)
		if n > maxBytes {
			if i == 0 {
				return ""
			}
			return string(runes[:i])
		}
	}
	return s
}

// sanitizePrompt ensures s is valid UTF-8. Copied from pkg/utils.SanitizePrompt.
func sanitizePrompt(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}

// wrapAsUserData wraps s in <user_data> delimiters. Copied from pkg/utils.WrapAsUserData.
func wrapAsUserData(s string) string {
	if s == "" {
		return "<user_data></user_data>"
	}
	return "<user_data>\n" + s + "\n</user_data>"
}

// parseKeyValueMap parses key/value text (no JSON). Returns:
//   - simple: key -> value for lines like "key: value" (keys lowercased)
//   - sections: section name -> lines for block sections like "entities:" followed by items
//
// Copied from pkg/utils.ParseKeyValueMap.
func parseKeyValueMap(text string) (simple map[string]string, sections map[string][]string) {
	simple = make(map[string]string)
	sections = make(map[string][]string)
	lines := strings.Split(text, "\n")
	var currentSection string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if currentSection != "" {
			sections[currentSection] = append(sections[currentSection], line)
			continue
		}
		idx := strings.Index(line, ":")
		if idx >= 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			keyLower := strings.ToLower(key)
			if value == "" {
				currentSection = keyLower
				continue
			}
			currentSection = ""
			simple[keyLower] = value
		}
	}
	return simple, sections
}
