package utils

import (
	"strings"
	"unicode/utf8"
)

// SanitizePrompt ensures a string is valid UTF-8 and removes any partial multi-byte characters.
// Use before sending text to Gemini; Protobuf rejects invalid UTF-8.
func SanitizePrompt(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}

// UserDataDelimOpen and UserDataDelimClose wrap user- or external-origin content in prompts.
const (
	UserDataDelimOpen  = "<user_data>"
	UserDataDelimClose = "</user_data>"
)

// WrapAsUserData wraps s in the standard user-data delimiters for prompt-injection mitigation.
func WrapAsUserData(s string) string {
	if s == "" {
		return UserDataDelimOpen + UserDataDelimClose
	}
	return UserDataDelimOpen + "\n" + s + "\n" + UserDataDelimClose
}

// TruncateString truncates s to at most maxRunes runes. Used for logging and previews.
func TruncateString(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

// FirstSentence returns the first sentence of s, or up to maxChars runes if no period found.
func FirstSentence(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	runes := []rune(s)
	for i, r := range runes {
		if r == '.' || r == '!' || r == '?' {
			return strings.TrimSpace(string(runes[:i+1]))
		}
	}
	if len(runes) <= maxChars {
		return string(runes)
	}
	return string(runes[:maxChars]) + "..."
}
