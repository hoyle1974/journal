package utils

import (
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"empty", "", 10, ""},
		{"shorter than max", "hello", 10, "hello"},
		{"equal to max", "12345", 5, "12345"},
		{"longer than max", "hello world", 5, "hello"},
		{"unicode", "café", 3, "caf"},
		{"max zero", "abc", 0, ""},
		{"emoji", "hello😀", 5, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateString(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("TruncateString(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestGenerateRandom(t *testing.T) {
	t.Parallel()

	t.Run("number format and bounds", func(t *testing.T) {
		t.Parallel()
		got := GenerateRandom("number", 10, 20, "")
		if !strings.HasPrefix(got, "Random number (") {
			t.Errorf("expected prefix 'Random number (', got %q", got)
		}
		// Extract the actual number from the end of the string: "Random number (10-20): N"
		parts := strings.Split(got, ": ")
		n, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil {
			t.Fatalf("could not parse number from output %q: %v", got, err)
		}
		if n < 10 || n > 20 {
			t.Errorf("number %d out of range [10, 20]", n)
		}
	})

	t.Run("number maxVal <= minVal resets to minVal+100", func(t *testing.T) {
		t.Parallel()
		got := GenerateRandom("number", 5, 5, "")
		if !strings.HasPrefix(got, "Random number (") {
			t.Errorf("expected prefix 'Random number (', got %q", got)
		}
		// maxVal should have been reset to 5+100=105; output should reflect that range
		if !strings.Contains(got, "5-105") {
			t.Errorf("expected range 5-105 in output, got %q", got)
		}
	})

	t.Run("uuid format", func(t *testing.T) {
		t.Parallel()
		got := GenerateRandom("uuid", 0, 0, "")
		if !strings.HasPrefix(got, "Random UUID: ") {
			t.Errorf("expected prefix 'Random UUID: ', got %q", got)
		}
		uuidPart := strings.TrimPrefix(got, "Random UUID: ")
		if _, err := uuid.Parse(uuidPart); err != nil {
			t.Errorf("UUID part %q is not a valid UUID: %v", uuidPart, err)
		}
	})

	t.Run("pick returns one of the choices", func(t *testing.T) {
		t.Parallel()
		got := GenerateRandom("pick", 0, 0, "apple, banana, cherry")
		if !strings.HasPrefix(got, "Picked: ") {
			t.Errorf("expected prefix 'Picked: ', got %q", got)
		}
		valid := []string{"apple", "banana", "cherry"}
		found := false
		for _, v := range valid {
			if strings.Contains(got, v) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("output %q does not contain any of the choices %v", got, valid)
		}
		if !strings.Contains(got, "(from 3 choices)") {
			t.Errorf("expected '(from 3 choices)' in output, got %q", got)
		}
	})

	t.Run("pick with empty choices returns error string", func(t *testing.T) {
		t.Parallel()
		got := GenerateRandom("pick", 0, 0, "")
		want := "Error: 'choices' parameter required for pick"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("coin returns heads or tails", func(t *testing.T) {
		t.Parallel()
		got := GenerateRandom("coin", 0, 0, "")
		if got != "Coin flip: Heads" && got != "Coin flip: Tails" {
			t.Errorf("unexpected coin result: %q", got)
		}
	})

	t.Run("dice returns 1-6", func(t *testing.T) {
		t.Parallel()
		got := GenerateRandom("dice", 0, 0, "")
		if !strings.HasPrefix(got, "Dice roll: ") {
			t.Errorf("expected prefix 'Dice roll: ', got %q", got)
		}
		n, err := strconv.Atoi(strings.TrimPrefix(got, "Dice roll: "))
		if err != nil {
			t.Fatalf("could not parse dice value from %q: %v", got, err)
		}
		if n < 1 || n > 6 {
			t.Errorf("dice value %d out of range [1, 6]", n)
		}
	})

	t.Run("die alias same as dice", func(t *testing.T) {
		t.Parallel()
		got := GenerateRandom("die", 0, 0, "")
		if !strings.HasPrefix(got, "Dice roll: ") {
			t.Errorf("expected prefix 'Dice roll: ', got %q", got)
		}
	})

	t.Run("unknown type", func(t *testing.T) {
		t.Parallel()
		got := GenerateRandom("flipcoin", 0, 0, "")
		if !strings.Contains(got, "Unknown random type:") {
			t.Errorf("expected 'Unknown random type:' in output, got %q", got)
		}
	})

	t.Run("case insensitive NUMBER", func(t *testing.T) {
		t.Parallel()
		got := GenerateRandom("NUMBER", 1, 10, "")
		if !strings.HasPrefix(got, "Random number (") {
			t.Errorf("expected prefix 'Random number (', got %q", got)
		}
	})
}

func TestEncodeDecodeText(t *testing.T) {
	t.Parallel()

	t.Run("base64_encode output format", func(t *testing.T) {
		t.Parallel()
		got, err := EncodeDecodeText("base64_encode", "hello world")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(got, "Base64 encoded:\n") {
			t.Errorf("expected prefix 'Base64 encoded:\\n', got %q", got)
		}
		encoded := strings.TrimPrefix(got, "Base64 encoded:\n")
		if encoded != base64.StdEncoding.EncodeToString([]byte("hello world")) {
			t.Errorf("encoded value mismatch: %q", encoded)
		}
	})

	t.Run("base64 round-trip", func(t *testing.T) {
		t.Parallel()
		original := "Hello, 世界!"
		encOut, err := EncodeDecodeText("base64_encode", original)
		if err != nil {
			t.Fatalf("encode error: %v", err)
		}
		encodedVal := strings.TrimPrefix(encOut, "Base64 encoded:\n")

		decOut, err := EncodeDecodeText("base64_decode", encodedVal)
		if err != nil {
			t.Fatalf("decode error: %v", err)
		}
		decoded := strings.TrimPrefix(decOut, "Base64 decoded:\n")
		if decoded != original {
			t.Errorf("round-trip got %q, want %q", decoded, original)
		}
	})

	t.Run("base64_decode invalid input returns error", func(t *testing.T) {
		t.Parallel()
		_, err := EncodeDecodeText("base64_decode", "not-valid-base64!!!")
		if err == nil {
			t.Fatal("expected error for invalid base64, got nil")
		}
		if !strings.Contains(err.Error(), "invalid base64") {
			t.Errorf("error %q does not contain 'invalid base64'", err.Error())
		}
	})

	t.Run("url_encode output format", func(t *testing.T) {
		t.Parallel()
		got, err := EncodeDecodeText("url_encode", "hello world & foo=bar")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(got, "URL encoded:\n") {
			t.Errorf("expected prefix 'URL encoded:\\n', got %q", got)
		}
		encoded := strings.TrimPrefix(got, "URL encoded:\n")
		if encoded != url.QueryEscape("hello world & foo=bar") {
			t.Errorf("url-encoded value mismatch: %q", encoded)
		}
	})

	t.Run("url round-trip", func(t *testing.T) {
		t.Parallel()
		original := "key=value&other=hello world"
		encOut, err := EncodeDecodeText("url_encode", original)
		if err != nil {
			t.Fatalf("encode error: %v", err)
		}
		encodedVal := strings.TrimPrefix(encOut, "URL encoded:\n")

		decOut, err := EncodeDecodeText("url_decode", encodedVal)
		if err != nil {
			t.Fatalf("decode error: %v", err)
		}
		decoded := strings.TrimPrefix(decOut, "URL decoded:\n")
		if decoded != original {
			t.Errorf("round-trip got %q, want %q", decoded, original)
		}
	})

	t.Run("url_decode invalid input returns error", func(t *testing.T) {
		t.Parallel()
		_, err := EncodeDecodeText("url_decode", "bad%ZZescaping")
		if err == nil {
			t.Fatal("expected error for invalid URL encoding, got nil")
		}
	})

	t.Run("json_format prettifies valid JSON", func(t *testing.T) {
		t.Parallel()
		got, err := EncodeDecodeText("json_format", `{"b":2,"a":1}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(got, "Formatted JSON:\n") {
			t.Errorf("expected prefix 'Formatted JSON:\\n', got %q", got)
		}
		// Prettified output should contain newlines and indentation
		body := strings.TrimPrefix(got, "Formatted JSON:\n")
		if !strings.Contains(body, "\n") {
			t.Errorf("expected multi-line formatted JSON, got %q", body)
		}
	})

	t.Run("json_format invalid JSON returns error", func(t *testing.T) {
		t.Parallel()
		_, err := EncodeDecodeText("json_format", `{not json}`)
		if err == nil {
			t.Fatal("expected error for invalid JSON, got nil")
		}
		if !strings.Contains(err.Error(), "invalid JSON") {
			t.Errorf("error %q does not contain 'invalid JSON'", err.Error())
		}
	})

	t.Run("json_minify minifies valid JSON", func(t *testing.T) {
		t.Parallel()
		got, err := EncodeDecodeText("json_minify", "{\n  \"a\": 1,\n  \"b\": 2\n}")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(got, "Minified JSON:\n") {
			t.Errorf("expected prefix 'Minified JSON:\\n', got %q", got)
		}
		body := strings.TrimPrefix(got, "Minified JSON:\n")
		if strings.Contains(body, "\n") || strings.Contains(body, "  ") {
			t.Errorf("expected compact JSON, got %q", body)
		}
	})

	t.Run("json_prettify alias same as json_format", func(t *testing.T) {
		t.Parallel()
		got, err := EncodeDecodeText("json_prettify", `{"x":1}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(got, "Formatted JSON:\n") {
			t.Errorf("expected prefix 'Formatted JSON:\\n', got %q", got)
		}
	})

	t.Run("unknown operation returns error", func(t *testing.T) {
		t.Parallel()
		_, err := EncodeDecodeText("rot13", "hello")
		if err == nil {
			t.Fatal("expected error for unknown operation, got nil")
		}
		if !strings.Contains(err.Error(), "unknown operation:") {
			t.Errorf("error %q does not contain 'unknown operation:'", err.Error())
		}
	})

	t.Run("case insensitive BASE64_ENCODE", func(t *testing.T) {
		t.Parallel()
		got, err := EncodeDecodeText("BASE64_ENCODE", "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(got, "Base64 encoded:\n") {
			t.Errorf("expected prefix 'Base64 encoded:\\n', got %q", got)
		}
	})
}

func TestTruncateToMaxBytes(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		maxBytes int
		want     string
	}{
		{"empty", "", 10, ""},
		{"shorter", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"emoji no cut", "hello😀", 8, "hello"},
		{"emoji fits", "hello😀", 9, "hello😀"},
		{"café", "café", 5, "café"},
		{"café cut", "café", 4, "caf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateToMaxBytes(tt.s, tt.maxBytes)
			if got != tt.want {
				t.Errorf("TruncateToMaxBytes(%q, %d) = %q, want %q", tt.s, tt.maxBytes, got, tt.want)
			}
		})
	}
}
