package jot

import (
	"testing"

	"github.com/jackstrohm/jot/internal/config"
)

func TestNormalizePhoneNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "already E.164 format",
			input:    "+15551234567",
			expected: "+15551234567",
		},
		{
			name:     "10 digit US number",
			input:    "5551234567",
			expected: "+15551234567",
		},
		{
			name:     "with dashes",
			input:    "555-123-4567",
			expected: "+15551234567",
		},
		{
			name:     "with parentheses",
			input:    "(555) 123-4567",
			expected: "+15551234567",
		},
		{
			name:     "with country code no plus",
			input:    "15551234567",
			expected: "+15551234567",
		},
		{
			name:     "international format",
			input:    "+44 20 7946 0958",
			expected: "+442079460958",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizePhoneNumber(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizePhoneNumber(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsAllowedPhoneNumber(t *testing.T) {
	restore := SetTestConfig(&config.Config{AllowedPhoneNumber: "+15551234567"})
	defer restore()

	tests := []struct {
		name     string
		phone    string
		expected bool
	}{
		{
			name:     "exact match",
			phone:    "+15551234567",
			expected: true,
		},
		{
			name:     "without plus prefix",
			phone:    "15551234567",
			expected: true,
		},
		{
			name:     "10 digit format",
			phone:    "5551234567",
			expected: true,
		},
		{
			name:     "with formatting",
			phone:    "(555) 123-4567",
			expected: true,
		},
		{
			name:     "different number",
			phone:    "+15559876543",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAllowedPhoneNumber(tt.phone)
			if result != tt.expected {
				t.Errorf("IsAllowedPhoneNumber(%q) = %v, want %v", tt.phone, result, tt.expected)
			}
		})
	}
}

func TestIsAllowedPhoneNumber_Empty(t *testing.T) {
	restore := SetTestConfig(&config.Config{AllowedPhoneNumber: ""})
	defer restore()

	// When AllowedPhoneNumber is empty, all numbers should be allowed
	if !IsAllowedPhoneNumber("+15551234567") {
		t.Error("IsAllowedPhoneNumber should allow any number when AllowedPhoneNumber is empty")
	}
}
