package jot

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackstrohm/jot/internal/api"
)

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		remote   string
		expected string
	}{
		{
			name:     "X-Forwarded-For single",
			headers:  map[string]string{"X-Forwarded-For": "192.168.1.1"},
			remote:   "10.0.0.1",
			expected: "192.168.1.1",
		},
		{
			name:     "X-Forwarded-For multiple",
			headers:  map[string]string{"X-Forwarded-For": "203.0.113.1, 70.41.3.18, 150.172.238.178"},
			remote:   "10.0.0.1",
			expected: "203.0.113.1",
		},
		{
			name:     "X-Real-IP",
			headers:  map[string]string{"X-Real-IP": "172.16.0.1"},
			remote:   "10.0.0.1",
			expected: "172.16.0.1",
		},
		{
			name:     "fallback to RemoteAddr",
			headers:  map[string]string{},
			remote:   "192.168.1.100:54321",
			expected: "192.168.1.100:54321",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remote
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			got := getClientIP(r)
			if got != tt.expected {
				t.Errorf("getClientIP() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestRateLimitPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/query", "/query"},
		{"/entries", "/entries"},
		{"/entries/abc-123", "/entries"},
		{"/entries/abc-123/foo", "/entries"},
		{"/health", "/health"},
		{"/", "/health"},
		{"", "/health"},
		{"/plan", "/plan"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := api.RateLimitPath(tt.path)
			if got != tt.expected {
				t.Errorf("rateLimitPath(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestCheckRateLimit_AllowsWithinLimit(t *testing.T) {
	r, _ := http.NewRequest("GET", "/query", nil)
	r.Header.Set("X-Forwarded-For", "192.168.1.1")

	// First request should succeed
	if !checkRateLimit(r, "/query") {
		t.Error("first request should be allowed")
	}
}

func TestCheckRateLimit_ExceedsLimit(t *testing.T) {
	// Use unique IP per test to avoid interference from other tests
	testIP := uuid.New().String()

	// /query limit is 30/min - exhaust the limit
	for i := 0; i < 30; i++ {
		r, _ := http.NewRequest("GET", "/query", nil)
		r.Header.Set("X-Forwarded-For", testIP)
		if !checkRateLimit(r, "/query") {
			t.Errorf("request %d should be allowed (within limit)", i+1)
		}
	}

	// 31st request should be rate limited
	r, _ := http.NewRequest("GET", "/query", nil)
	r.Header.Set("X-Forwarded-For", testIP)
	if checkRateLimit(r, "/query") {
		t.Error("31st request should be rate limited")
	}
}
