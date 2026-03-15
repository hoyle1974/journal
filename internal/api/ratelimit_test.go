package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
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
			got := GetClientIP(r)
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
			got := RateLimitPath(tt.path)
			if got != tt.expected {
				t.Errorf("rateLimitPath(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestRateLimitMiddleware_AllowsWithinLimit(t *testing.T) {
	r := chi.NewRouter()
	r.With(RateLimitMiddleware(5)).Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1"
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusTooManyRequests {
		t.Error("first request should not be rate limited")
	}
}

func TestRateLimitMiddleware_ExceedsLimit(t *testing.T) {
	r := chi.NewRouter()
	r.With(RateLimitMiddleware(2)).Get("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	const testIP = "10.0.0.99"
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = testIP
		r.ServeHTTP(rec, req)
		if i < 2 {
			if rec.Code == http.StatusTooManyRequests {
				t.Errorf("request %d should be allowed (within limit of 2/min), got 429", i+1)
			}
		} else {
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("3rd request should be rate limited, got %d", rec.Code)
			}
		}
	}
}
