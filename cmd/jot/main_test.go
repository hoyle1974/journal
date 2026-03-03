package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJsonStr(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected string
	}{
		{"nil map", nil, "x", ""},
		{"missing key", map[string]interface{}{"a": "b"}, "x", ""},
		{"string value", map[string]interface{}{"msg": "hello"}, "msg", "hello"},
		{"empty string", map[string]interface{}{"msg": ""}, "msg", ""},
		{"non-string value", map[string]interface{}{"num": 42}, "num", ""},
		{"nil value", map[string]interface{}{"x": nil}, "x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonStr(tt.m, tt.key)
			if got != tt.expected {
				t.Errorf("jsonStr() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestJsonFloat(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected float64
	}{
		{"nil map", nil, "x", 0},
		{"missing key", map[string]interface{}{"a": 1.0}, "x", 0},
		{"float value", map[string]interface{}{"n": 42.5}, "n", 42.5},
		{"int from JSON", map[string]interface{}{"n": float64(10)}, "n", 10},
		{"zero", map[string]interface{}{"n": 0.0}, "n", 0},
		{"non-number value", map[string]interface{}{"n": "bad"}, "n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonFloat(tt.m, tt.key)
			if got != tt.expected {
				t.Errorf("jsonFloat() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestApiRequest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true, "uuid": "abc-123"}`))
	}))
	defer server.Close()

	origURL := APIBaseURL
	origKey := APIKey
	APIBaseURL = server.URL
	APIKey = "test-key"
	defer func() {
		APIBaseURL = origURL
		APIKey = origKey
	}()

	result, err := apiRequest("POST", "/log", map[string]string{"content": "test"}, 5*time.Second)
	if err != nil {
		t.Fatalf("apiRequest() error: %v", err)
	}
	if result == nil {
		t.Fatal("apiRequest() returned nil result")
	}
	if jsonStr(result, "uuid") != "abc-123" {
		t.Errorf("uuid = %q, want abc-123", jsonStr(result, "uuid"))
	}
}

func TestApiRequest_4xxError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "Invalid API key"}`))
	}))
	defer server.Close()

	origURL := APIBaseURL
	APIBaseURL = server.URL
	defer func() { APIBaseURL = origURL }()

	result, err := apiRequest("GET", "/entries", nil, 5*time.Second)
	if err == nil {
		t.Fatal("apiRequest() expected error for 401")
	}
	if result != nil {
		t.Error("apiRequest() should return nil result on error")
	}
	if err.Error() != "API error 401: Invalid API key" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestApiRequest_5xxError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "server meltdown"}`))
	}))
	defer server.Close()

	origURL := APIBaseURL
	APIBaseURL = server.URL
	defer func() { APIBaseURL = origURL }()

	_, err := apiRequest("POST", "/query", nil, 5*time.Second)
	if err == nil {
		t.Fatal("apiRequest() expected error for 500")
	}
	if err.Error() != "API error 500: server meltdown" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestApiRequest_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	origURL := APIBaseURL
	APIBaseURL = server.URL
	defer func() { APIBaseURL = origURL }()

	_, err := apiRequest("GET", "/health", nil, 5*time.Second)
	if err == nil {
		t.Fatal("apiRequest() expected error for invalid JSON")
	}
}
