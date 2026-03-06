package utils

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// TruncateToMaxBytes truncates s to at most maxBytes bytes, never cutting a multi-byte rune in half.
func TruncateToMaxBytes(s string, maxBytes int) string {
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

// GenerateRandom generates random values.
func GenerateRandom(randType string, minVal, maxVal int, choices string) string {
	switch strings.ToLower(randType) {
	case "number":
		if maxVal <= minVal {
			maxVal = minVal + 100
		}
		n := rand.Intn(maxVal-minVal+1) + minVal
		return fmt.Sprintf("Random number (%d-%d): %d", minVal, maxVal, n)

	case "uuid":
		return fmt.Sprintf("Random UUID: %s", uuid.New().String())

	case "pick":
		if choices == "" {
			return "Error: 'choices' parameter required for pick"
		}
		items := strings.Split(choices, ",")
		for i := range items {
			items[i] = strings.TrimSpace(items[i])
		}
		pick := items[rand.Intn(len(items))]
		return fmt.Sprintf("Picked: %s (from %d choices)", pick, len(items))

	case "coin":
		if rand.Intn(2) == 0 {
			return "Coin flip: Heads"
		}
		return "Coin flip: Tails"

	case "dice", "die":
		n := rand.Intn(6) + 1
		return fmt.Sprintf("Dice roll: %d", n)

	default:
		return fmt.Sprintf("Unknown random type: %s (use: number, uuid, pick, coin, dice)", randType)
	}
}

// EncodeDecodeText performs encoding/decoding operations.
func EncodeDecodeText(operation, text string) (string, error) {
	switch strings.ToLower(operation) {
	case "base64_encode":
		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		return fmt.Sprintf("Base64 encoded:\n%s", encoded), nil

	case "base64_decode":
		decoded, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return "", fmt.Errorf("invalid base64: %v", err)
		}
		return fmt.Sprintf("Base64 decoded:\n%s", string(decoded)), nil

	case "url_encode":
		encoded := url.QueryEscape(text)
		return fmt.Sprintf("URL encoded:\n%s", encoded), nil

	case "url_decode":
		decoded, err := url.QueryUnescape(text)
		if err != nil {
			return "", fmt.Errorf("invalid URL encoding: %v", err)
		}
		return fmt.Sprintf("URL decoded:\n%s", decoded), nil

	case "json_format", "json_prettify":
		var data interface{}
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			return "", fmt.Errorf("invalid JSON: %v", err)
		}
		formatted, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Formatted JSON:\n%s", string(formatted)), nil

	case "json_minify":
		var data interface{}
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			return "", fmt.Errorf("invalid JSON: %v", err)
		}
		minified, err := json.Marshal(data)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Minified JSON:\n%s", string(minified)), nil

	default:
		return "", fmt.Errorf("unknown operation: %s (use: base64_encode, base64_decode, url_encode, url_decode, json_format, json_minify)", operation)
	}
}
