package api

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/internal/config"
)

func testAppForAPI() *infra.App {
	return &infra.App{Logger: slog.Default()}
}

func testServerForAPI(cfg *config.Config) *Server {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return NewServer(testAppForAPI(), cfg, slog.Default(), nil, nil, nil, nil, Router)
}

func TestCheckAuth_NoKeyConfigured(t *testing.T) {
	s := testServerForAPI(&config.Config{JotAPIKey: ""})
	r, _ := http.NewRequest("GET", "/query", nil)
	code, msg := CheckAuth(s, r)
	if code != 0 {
		t.Errorf("checkAuth() with no key configured: code = %d, want 0", code)
	}
	if msg != "" {
		t.Errorf("checkAuth() with no key configured: msg = %q, want empty", msg)
	}
}

func TestCheckAuth_MissingHeader(t *testing.T) {
	s := testServerForAPI(&config.Config{JotAPIKey: "secret"})
	r, _ := http.NewRequest("GET", "/query", nil)
	code, msg := CheckAuth(s, r)
	if code != http.StatusUnauthorized {
		t.Errorf("checkAuth() missing header: code = %d, want 401", code)
	}
	if msg != "Missing X-API-Key header" {
		t.Errorf("checkAuth() missing header: msg = %q", msg)
	}
}

func TestCheckAuth_InvalidKey(t *testing.T) {
	s := testServerForAPI(&config.Config{JotAPIKey: "correct-key"})
	r, _ := http.NewRequest("GET", "/query", nil)
	r.Header.Set("X-API-Key", "wrong-key")
	code, msg := CheckAuth(s, r)
	if code != http.StatusForbidden {
		t.Errorf("checkAuth() invalid key: code = %d, want 403", code)
	}
	if msg != "Invalid API key" {
		t.Errorf("checkAuth() invalid key: msg = %q", msg)
	}
}

func TestCheckAuth_ValidKey(t *testing.T) {
	s := testServerForAPI(&config.Config{JotAPIKey: "secret"})
	r, _ := http.NewRequest("GET", "/query", nil)
	r.Header.Set("X-API-Key", "secret")
	code, msg := CheckAuth(s, r)
	if code != 0 {
		t.Errorf("checkAuth() valid key: code = %d, want 0", code)
	}
	if msg != "" {
		t.Errorf("checkAuth() valid key: msg = %q, want empty", msg)
	}
}

func TestPublicRoutes(t *testing.T) {
	publicPaths := []string{"", "/health", "/metrics", "/webhook", "/sms", "/privacy-policy", "/terms-and-conditions"}
	for _, path := range publicPaths {
		if !PublicRoutes[path] {
			t.Errorf("path %q should be public", path)
		}
	}
	protectedPaths := []string{"/log", "/query", "/plan", "/entries", "/sync", "/dream", "/janitor", "/rollup", "/pending-questions"}
	for _, path := range protectedPaths {
		if PublicRoutes[path] {
			t.Errorf("path %q should be protected", path)
		}
	}
}
