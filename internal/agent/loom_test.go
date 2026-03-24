package agent

import (
	"context"
	"testing"
)

// TestProcessLogSequentialNilApp verifies the function returns a typed error (not a panic)
// when called with a nil app. Full integration tests require a Firestore emulator.
func TestProcessLogSequentialNilApp(t *testing.T) {
	_, err := ProcessLogSequential(context.Background(), nil, "test-uuid", "test content", "2026-01-01T00:00:00Z", "test")
	if err == nil {
		t.Fatal("expected error for nil app, got nil")
	}
}
