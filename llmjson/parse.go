// Package llmjson provides resilient parsing of JSON from LLM responses that may be
// truncated, have trailing commas, or other common malformations.
package llmjson

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Repair applies minimal heuristics to fix common LLM JSON issues without changing valid JSON.
// It trims whitespace, removes trailing commas before ] or }, and attempts to close
// truncated JSON by appending ]} as needed.
func Repair(text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return s
	}
	// Remove trailing comma before ] or }
	s = regexp.MustCompile(`,\s*([}\]])`).ReplaceAllString(s, "$1")
	// If still ends mid-stream, try to close. Count open braces/brackets.
	openBraces := 0
	openBrackets := 0
	inString := false
	escape := false
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if inString {
			if c == '\\' && quote != 0 {
				escape = true
				continue
			}
			if c == quote {
				inString = false
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inString = true
			quote = c
		case '{':
			openBraces++
		case '}':
			openBraces--
		case '[':
			openBrackets++
		case ']':
			openBrackets--
		}
	}
	// Append closing delimiters (typically object then array if used at top level)
	for openBrackets > 0 {
		s += "]"
		openBrackets--
	}
	for openBraces > 0 {
		s += "}"
		openBraces--
	}
	return s
}

// RepairAndUnmarshal trims and repairs text (trailing commas, close truncated JSON),
// then attempts json.Unmarshal. Returns the first successful parse.
func RepairAndUnmarshal(text string, v interface{}) error {
	repaired := Repair(text)
	if err := json.Unmarshal([]byte(repaired), v); err != nil {
		return err
	}
	return nil
}

// PartialUnmarshalObject does best-effort extraction of top-level keys from possibly
// truncated or malformed JSON. It returns a map of key -> json.RawMessage for each key
// in keys that could be parsed. Keys not found or invalid are omitted.
func PartialUnmarshalObject(text string, keys []string) (map[string]json.RawMessage, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return nil, nil
	}
	// Try strict parse first
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err == nil {
		// Filter to requested keys only
		out := make(map[string]json.RawMessage)
		for _, k := range keys {
			if v, ok := raw[k]; ok {
				out[k] = v
			}
		}
		return out, nil
	}
	// Repaired parse
	repaired := Repair(s)
	if err := json.Unmarshal([]byte(repaired), &raw); err == nil {
		out := make(map[string]json.RawMessage)
		for _, k := range keys {
			if v, ok := raw[k]; ok {
				out[k] = v
			}
		}
		return out, nil
	}
	// Best-effort: find "key": value for each key using a simple scan.
	out := make(map[string]json.RawMessage)
	for _, key := range keys {
		rawVal := extractKeyValue(s, key)
		if len(rawVal) > 0 {
			out[key] = rawVal
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// extractKeyValue finds the first occurrence of "<key>": <value> and returns
// the value as json.RawMessage. Value is parsed by finding the span of the value
// (string, number, array, object, true/false/null) after the colon.
func extractKeyValue(s, key string) json.RawMessage {
	pat := `"` + regexp.QuoteMeta(key) + `"\s*:\s*`
	re := regexp.MustCompile(pat)
	idx := re.FindStringIndex(s)
	if idx == nil {
		return nil
	}
	start := idx[1]
	// Parse value: skip whitespace, then consume one JSON value
	return extractOneValue(s[start:])
}

// extractOneValue returns the first complete JSON value from the start of s.
func extractOneValue(s string) json.RawMessage {
	s = strings.TrimLeft(s, " \t\n\r")
	if s == "" {
		return nil
	}
	switch s[0] {
	case '"':
		return extractString(s)
	case '{':
		return extractObject(s)
	case '[':
		return extractArray(s)
	case 't', 'f':
		if strings.HasPrefix(s, "true") {
			return json.RawMessage("true")
		}
		if strings.HasPrefix(s, "false") {
			return json.RawMessage("false")
		}
		return nil
	case 'n':
		if strings.HasPrefix(s, "null") {
			return json.RawMessage("null")
		}
		return nil
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return extractNumber(s)
	default:
		return nil
	}
}

func extractString(s string) json.RawMessage {
	if len(s) < 2 || s[0] != '"' {
		return nil
	}
	i := 1
	for i < len(s) {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == '"' {
			return json.RawMessage(s[:i+1])
		}
		i++
	}
	return nil
}

func extractNumber(s string) json.RawMessage {
	i := 0
	if s[i] == '-' {
		i++
	}
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	// optional exponent
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i > 0 {
		return json.RawMessage(s[:i])
	}
	return nil
}

func extractObject(s string) json.RawMessage {
	if len(s) < 2 || s[0] != '{' {
		return nil
	}
	depth := 1
	inString := false
	var quote byte
	escape := false
	i := 1
	for i < len(s) {
		c := s[i]
		if escape {
			escape = false
			i++
			continue
		}
		if inString {
			if c == '\\' {
				escape = true
				i++
				continue
			}
			if c == quote {
				inString = false
			}
			i++
			continue
		}
		switch c {
		case '"', '\'':
			inString = true
			quote = c
			i++
			continue
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return json.RawMessage(s[:i+1])
			}
		}
		i++
	}
	return nil
}

func extractArray(s string) json.RawMessage {
	if len(s) < 2 || s[0] != '[' {
		return nil
	}
	depth := 1
	inString := false
	var quote byte
	escape := false
	i := 1
	for i < len(s) {
		c := s[i]
		if escape {
			escape = false
			i++
			continue
		}
		if inString {
			if c == '\\' {
				escape = true
				i++
				continue
			}
			if c == quote {
				inString = false
			}
			i++
			continue
		}
		switch c {
		case '"', '\'':
			inString = true
			quote = c
			i++
			continue
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return json.RawMessage(s[:i+1])
			}
		}
		i++
	}
	return nil
}
