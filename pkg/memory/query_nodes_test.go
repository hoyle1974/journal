package memory

import (
	"context"
	"testing"
)

func TestSaveQuery_NilEnv(t *testing.T) {
	_, err := SaveQuery(context.Background(), nil, "q?", "a", "test", false)
	if err == nil {
		t.Fatal("expected error for nil env, got nil")
	}
}

func TestGetRecentQueries_NilEnv(t *testing.T) {
	_, err := GetRecentQueries(context.Background(), nil, 10)
	if err == nil {
		t.Fatal("expected error for nil env, got nil")
	}
}
