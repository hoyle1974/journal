package api

import (
	"testing"
)

func TestEntryUUIDRegex(t *testing.T) {
	tests := []struct {
		path    string
		matches bool
		uuid    string
	}{
		{"/entries/abc-123", true, "abc-123"},
		{"/entries/xyz-123", false, ""},
		{"/entries/a1b2c3d4-e5f6-7890-abcd-ef1234567890", true, "a1b2c3d4-e5f6-7890-abcd-ef1234567890"},
		{"/entries", false, ""},
		{"/entries/", false, ""},
		{"/entries/123e4567-e89b-12d3-a456-426614174000", true, "123e4567-e89b-12d3-a456-426614174000"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			match := EntryUUIDRegex.FindStringSubmatch(tt.path)
			if tt.matches {
				if len(match) != 2 {
					t.Errorf("expected match, got %v", match)
				} else if match[1] != tt.uuid {
					t.Errorf("uuid = %q, want %q", match[1], tt.uuid)
				}
			} else {
				if len(match) != 0 {
					t.Errorf("expected no match, got %v", match)
				}
			}
		})
	}
}
