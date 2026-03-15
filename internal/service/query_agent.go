package service

import (
	"context"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
)

const (
	MaxIterations       = 10
	MaxMessagePairs     = 20
	ToolRepeatBackOffAt = 3
)

// QueryResult is the result of a query. Re-exported from agent for callers.
type QueryResult = agent.QueryResult

// RunQuery runs a query against the journal using the agentic loop.
// app is the runtime env (implements agent.FOHEnv); pass explicitly instead of from context.
func RunQuery(ctx context.Context, app agent.FOHEnv, question, source string) *QueryResult {
	return RunQueryWithDebug(ctx, app, question, source, false)
}

// RunQueryWithDebug runs a query with optional debug logging.
func RunQueryWithDebug(ctx context.Context, app agent.FOHEnv, question, source string, debug bool) *QueryResult {
	return agent.RunQueryWithDebug(ctx, app, question, source, debug)
}

// CreateAndSavePlan forces Gemini to decompose a goal into JSON, then saves it to the Knowledge Graph.
func CreateAndSavePlan(ctx context.Context, env infra.ToolEnv, goal string) (string, error) {
	return agent.CreateAndSavePlan(ctx, env, goal)
}
