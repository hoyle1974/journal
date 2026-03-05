package utils

import (
	"testing"
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
