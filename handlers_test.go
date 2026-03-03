package jot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)

	handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleHealth status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "healthy") {
		t.Errorf("body should contain 'healthy', got %q", body)
	}
	if !strings.Contains(body, "timestamp") {
		t.Errorf("body should contain 'timestamp', got %q", body)
	}
}

func TestHandleMetrics(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)

	handleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleMetrics status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, key := range []string{"queries_total", "entries_total", "tool_calls_total", "gemini_calls_total", "errors_total"} {
		if !strings.Contains(body, key) {
			t.Errorf("body should contain %q, got %q", key, body)
		}
	}
}

func TestEntryUUIDRegex(t *testing.T) {
	tests := []struct {
		path    string
		matches bool
		uuid    string
	}{
		{"/entries/abc-123", true, "abc-123"}, // regex allows hex + hyphen
		{"/entries/xyz-123", false, ""},       // x,y,z not in [a-f0-9]
		{"/entries/a1b2c3d4-e5f6-7890-abcd-ef1234567890", true, "a1b2c3d4-e5f6-7890-abcd-ef1234567890"},
		{"/entries", false, ""},
		{"/entries/", false, ""},
		{"/entries/123e4567-e89b-12d3-a456-426614174000", true, "123e4567-e89b-12d3-a456-426614174000"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			match := entryUUIDRegex.FindStringSubmatch(tt.path)
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
