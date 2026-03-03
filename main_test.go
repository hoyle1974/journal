package jot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	data := map[string]string{"key": "value"}

	writeJSON(rec, http.StatusOK, data)

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
	writeJSON(rec, http.StatusNotFound, map[string]string{"error": "Not found"})

	if rec.Code != http.StatusNotFound {
		t.Errorf("writeJSON status = %d, want 404", rec.Code)
	}
}

func TestResponseWriter_CapturesStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	// Simulate handler writing 404
	rw.WriteHeader(http.StatusNotFound)
	rw.Write([]byte("not found"))

	if rw.statusCode != http.StatusNotFound {
		t.Errorf("responseWriter.statusCode = %d, want 404", rw.statusCode)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("underlying recorder status = %d, want 404", rec.Code)
	}
}
