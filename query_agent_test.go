package jot

import (
	"testing"
)

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		maxLen  int
		want    string
	}{
		{"empty", "", 10, ""},
		{"shorter than max", "hello", 10, "hello"},
		{"equal to max", "12345", 5, "12345"},
		{"longer than max", "hello world", 5, "hello"},
		{"unicode", "café", 3, "caf"},
		{"max zero", "abc", 0, ""},
		{"emoji", "hello😀", 5, "hello"}, // must not cut emoji in half (invalid UTF-8)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateString(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateString(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
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
		{"emoji no cut", "hello😀", 8, "hello"},   // emoji is 4 bytes; 5+4=9 > 8, so stop at "hello"
		{"emoji fits", "hello😀", 9, "hello😀"},
		{"café", "café", 5, "café"},               // é is 2 bytes
		{"café cut", "café", 4, "caf"},            // can't fit é (2 bytes)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateToMaxBytes(tt.s, tt.maxBytes)
			if got != tt.want {
				t.Errorf("truncateToMaxBytes(%q, %d) = %q, want %q", tt.s, tt.maxBytes, got, tt.want)
			}
		})
	}
}

func TestLooksLikeQuestion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"question mark", "What is this?", true},
		{"question mark only", "?", true},
		{"what prefix", "what did I do yesterday", true},
		{"what's prefix", "what's the weather", true},
		{"how prefix", "how do I do this", true},
		{"who prefix", "who knows about GCP", true},
		{"tell me", "tell me about my entries", true},
		{"show me", "show me my todos", true},
		{"is prefix", "is this a question", true},
		{"do prefix", "do I have any meetings", true},
		{"plain statement", "Had coffee with Sarah", false},
		{"plain statement 2", "I want to learn Japanese", false},
		{"empty", "", false},
		{"whitespace", "   ", false},
		{"mixed case what", "WHAT is this", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeQuestion(tt.input)
			if got != tt.expected {
				t.Errorf("looksLikeQuestion(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
