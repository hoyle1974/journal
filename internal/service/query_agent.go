package service

import (
	"context"

	"github.com/jackstrohm/jot/internal/agent"
)

const (
	MaxIterations       = 10
	MaxMessagePairs     = 20
	ToolRepeatBackOffAt = 3
)

// RunQuery runs a query against the journal using the agentic loop.
// app is the runtime env (implements agent.FOHEnv); pass explicitly instead of from context.
func RunQuery(ctx context.Context, app agent.FOHEnv, question, source string) *agent.QueryResult {
	return RunQueryWithDebug(ctx, app, question, source, false)
}

// RunQueryWithDebug runs a query with optional debug logging.
func RunQueryWithDebug(ctx context.Context, app agent.FOHEnv, question, source string, debug bool) *agent.QueryResult {
	return agent.RunQueryWithDebug(ctx, app, question, source, debug)
}

