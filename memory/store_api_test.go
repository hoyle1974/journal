package memory

import "testing"

// TestStoreImplementsAllDomainInterfaces verifies at compile time that *Store
// satisfies every domain interface. The var declarations fail at compile time if
// any wrapper method is missing — they do not need to run to have effect.
// TaskStore uses exact method names (CreateTask, GetTask, etc.) so no wrappers
// are needed; its sub-test passes immediately after Task 2.
func TestStoreImplementsAllDomainInterfaces(t *testing.T) {
	tests := []struct {
		name  string
		check func()
	}{
		{"EntryStore",     func() { var _ EntryStore     = (*Store)(nil) }},
		{"KnowledgeStore", func() { var _ KnowledgeStore = (*Store)(nil) }},
		{"GraphStore",     func() { var _ GraphStore     = (*Store)(nil) }},
		{"TaskStore",      func() { var _ TaskStore      = (*Store)(nil) }},
		{"ContextStore",   func() { var _ ContextStore   = (*Store)(nil) }},
		{"AgentOps",       func() { var _ AgentOps       = (*Store)(nil) }},
		{"AdminOps",       func() { var _ AdminOps       = (*Store)(nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check() })
	}
}
