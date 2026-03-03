package jot

import (
	"strings"
	"unicode/utf8"
)

// truncateToMaxBytes truncates s to at most maxBytes bytes, never cutting a multi-byte rune in half.
// Use for byte-limited content (e.g. prompts) to avoid invalid UTF-8 that Gemini rejects.
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

// SanitizePrompt ensures a string is valid UTF-8 and removes any partial multi-byte characters.
// Use before sending text to Gemini; Protobuf rejects invalid UTF-8.
// This is for encoding only and does NOT protect against prompt injection; mitigation is
// handled by WrapAsUserData and system-prompt instructions at prompt construction sites.
func SanitizePrompt(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}

// UserDataDelimOpen and UserDataDelimClose wrap user- or external-origin content in prompts
// so the model treats it as data only, not instructions. Used with system-prompt instructions.
const (
	UserDataDelimOpen  = "<user_data>"
	UserDataDelimClose = "</user_data>"
)

// WrapAsUserData wraps s in the standard user-data delimiters for prompt-injection mitigation.
// Use when embedding user- or external-origin content (entries, queries, logs, tool results text)
// into prompt text sent to the LLM. Do not use for storage or non-LLM output.
func WrapAsUserData(s string) string {
	if s == "" {
		return UserDataDelimOpen + UserDataDelimClose
	}
	return UserDataDelimOpen + "\n" + s + "\n" + UserDataDelimClose
}

// SafeTruncate slices a string based on rune count, not byte count, to prevent splitting emojis.
func SafeTruncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

// truncateString truncates s to at most maxRunes runes. Used for logging and previews.
func truncateString(s string, maxRunes int) string {
	return SafeTruncate(s, maxRunes)
}
