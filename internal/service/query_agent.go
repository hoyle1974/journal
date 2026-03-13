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
// app is the runtime app (Firestore, Gemini, pools); pass explicitly instead of from context.
func RunQuery(ctx context.Context, app *infra.App, question, source string) *QueryResult {
	return RunQueryWithDebug(ctx, app, question, source, false)
}

// RunQueryWithDebug runs a query with optional debug logging.
func RunQueryWithDebug(ctx context.Context, app *infra.App, question, source string, debug bool) *QueryResult {
	return agent.RunQueryWithDebug(ctx, app, question, source, debug)
}

// CreateAndSavePlan forces Gemini to decompose a goal into JSON, then saves it to the Knowledge Graph.
func CreateAndSavePlan(ctx context.Context, app *infra.App, goal string) (string, error) {
	ctx = infra.WithApp(ctx, app)
	return agent.CreateAndSavePlan(ctx, goal)
}
