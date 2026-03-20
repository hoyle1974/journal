package memory

import (
	"context"
	"testing"
)

func TestCreateTask_NilEnv(t *testing.T) {
	_, err := CreateTask(context.Background(), nil, &Task{Content: "test"})
	if err == nil {
		t.Fatal("expected error for nil env, got nil")
	}
}

func TestCreateTask_EmptyContent(t *testing.T) {
	_, err := CreateTask(context.Background(), nil, &Task{})
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
}

func TestGetTask_NilEnv(t *testing.T) {
	_, err := GetTask(context.Background(), nil, "some-uuid")
	if err == nil {
		t.Fatal("expected error for nil env, got nil")
	}
}

func TestFormatTasksForContext_Empty(t *testing.T) {
	result := FormatTasksForContext(nil, 1000)
	if result != "No tasks found." {
		t.Errorf("expected 'No tasks found.', got %q", result)
	}
}

func TestNormalizeTaskStatus(t *testing.T) {
	cases := []struct{ in, want string }{
		{"pending", TaskStatusPending},
		{"active", TaskStatusActive},
		{"completed", TaskStatusCompleted},
		{"abandoned", TaskStatusAbandoned},
		{"", TaskStatusPending},
		{"INVALID", TaskStatusPending},
	}
	for _, c := range cases {
		if got := NormalizeTaskStatus(c.in); got != c.want {
			t.Errorf("NormalizeTaskStatus(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
