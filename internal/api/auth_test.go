package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/pkg/infra"
	"log/slog"
)

func testAppForAPI() *infra.App {
	return &infra.App{Logger: slog.Default()}
}

func testServerForAPI(cfg *config.Config) *Server {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return NewServer(testAppForAPI(), cfg, slog.Default(), nil, nil, nil, nil, nil)
}

func TestAuth_NoKeyConfigured(t *testing.T) {
	s := testServerForAPI(&config.Config{JotAPIKey: ""})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/log", nil)
	r.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(rec, r)
	// No key configured: request is allowed through (may get 400 for empty body)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Errorf("with no JOT_API_KEY configured, request should not be rejected with auth error; got %d", rec.Code)
	}
}

func TestAuth_MissingHeader(t *testing.T) {
	s := testServerForAPI(&config.Config{JotAPIKey: "secret"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/log", nil)
	r.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing X-API-Key: got status %d, want 401", rec.Code)
	}
}

func TestAuth_InvalidKey(t *testing.T) {
	s := testServerForAPI(&config.Config{JotAPIKey: "correct-key"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/log", nil)
	r.Header.Set("X-API-Key", "wrong-key")
	r.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("invalid key: got status %d, want 403", rec.Code)
	}
}

func TestAuth_ValidKey(t *testing.T) {
	s := testServerForAPI(&config.Config{JotAPIKey: "secret"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/log", nil)
	r.Header.Set("X-API-Key", "secret")
	r.Header.Set("Content-Type", "application/json")
	r.Body = http.NoBody
	s.ServeHTTP(rec, r)
	// Valid key: should get past auth; we expect 400 for empty/invalid body, not 401/403
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Errorf("valid key: got auth error %d, expected to pass auth", rec.Code)
	}
}
