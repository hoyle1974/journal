package jot

import (
	"context"

	"github.com/jackstrohm/jot/pkg/agent"
)

// buildSystemPrompt creates the system prompt with current date context and recent history.
func buildSystemPrompt(ctx context.Context) string {
	return agent.BuildSystemPrompt(ctx, jotFOHEnv{})
}
