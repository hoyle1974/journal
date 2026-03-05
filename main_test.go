package jot

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/internal/config"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	data := map[string]string{"key": "value"}

	api.WriteJSON(rec, http.StatusOK, data)

	if rec.Code != http.StatusOK {
		t.Errorf("writeJSON status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var decoded map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&decoded); err != nil {
		t.Fatalf("writeJSON produced invalid JSON: %v", err)
	}
	if decoded["key"] != "value" {
		t.Errorf("decoded[key] = %q, want value", decoded["key"])
	}
}

func TestWriteJSON_ErrorStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	api.WriteJSON(rec, http.StatusNotFound, map[string]string{"error": "Not found"})

	if rec.Code != http.StatusNotFound {
		t.Errorf("writeJSON status = %d, want 404", rec.Code)
	}
}

// Test that the API router returns 404 for unknown paths (responseWriter captures status).
func TestRouter_NotFound(t *testing.T) {
	srv := api.NewServer(testApp(), &config.Config{}, slog.Default(), nil, api.Router)
	rec := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "/unknown-path", nil)
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Errorf("ServeHTTP(unknown path) status = %d, want 404", rec.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	srv := testServer(&config.Config{GoogleCloudProject: "test-project"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /health status = %d, want 200", rec.Code)
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
	srv := testServer(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /metrics status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, key := range []string{"queries_total", "entries_total", "tool_calls_total", "gemini_calls_total", "errors_total"} {
		if !strings.Contains(body, key) {
			t.Errorf("body should contain %q, got %q", key, body)
		}
	}
}
