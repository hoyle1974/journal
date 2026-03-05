package infra

import (
	"testing"
)

func TestNormalizePhoneNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"already E.164 format", "+15551234567", "+15551234567"},
		{"10 digit US number", "5551234567", "+15551234567"},
		{"with dashes", "555-123-4567", "+15551234567"},
		{"with parentheses", "(555) 123-4567", "+15551234567"},
		{"with country code no plus", "15551234567", "+15551234567"},
		{"international format", "+44 20 7946 0958", "+442079460958"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizePhoneNumber(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizePhoneNumber(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
