package persona

import (
	"context"
	"testing"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/memory"
	"google.golang.org/genai"
)

// stubEnv implements infra.ToolEnv with a configurable Config(). Firestore,
// Dispatch, and MemoryStore panic if called — the edge-case paths under test never reach them.
type stubEnv struct{ cfg *config.Config }

func (s *stubEnv) Config() *config.Config { return s.cfg }
func (s *stubEnv) Firestore(_ context.Context) (*firestore.Client, error) {
	panic("Firestore called unexpectedly")
}
func (s *stubEnv) Dispatch(_ context.Context, _ *infra.LLMRequest) (*genai.GenerateContentResponse, error) {
	panic("Dispatch called unexpectedly")
}
func (s *stubEnv) MemoryStore() *memory.Store {
	panic("MemoryStore called unexpectedly")
}

func TestApplyEdgeCases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("empty string returns empty", func(t *testing.T) {
		t.Parallel()
		got := Apply(ctx, nil, "", "")
		if got != "" {
			t.Errorf("Apply(empty) = %q, want empty string", got)
		}
	})

	t.Run("whitespace-only returns empty after TrimSpace", func(t *testing.T) {
		t.Parallel()
		// Use a real stubEnv so the test isolates the TrimSpace guard, not the nil-env guard.
		env := &stubEnv{cfg: &config.Config{}}
		got := Apply(ctx, env, "   \t\n  ", "")
		if got != "" {
			t.Errorf("Apply(whitespace) = %q, want empty string", got)
		}
	})

	t.Run("nil env returns rawText unchanged", func(t *testing.T) {
		t.Parallel()
		got := Apply(ctx, nil, "hello world", "")
		if got != "hello world" {
			t.Errorf("Apply(nilEnv) = %q, want %q", got, "hello world")
		}
	})

	t.Run("env with nil Config returns rawText unchanged", func(t *testing.T) {
		t.Parallel()
		env := &stubEnv{cfg: nil}
		got := Apply(ctx, env, "hello world", "")
		if got != "hello world" {
			t.Errorf("Apply(nilConfig) = %q, want %q", got, "hello world")
		}
	})
}
