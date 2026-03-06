package service

import (
	"context"
	"strings"

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

// GetAnswer returns just the answer string (for sync compatibility).
func GetAnswer(ctx context.Context, app *infra.App, question, source string) string {
	result := RunQuery(ctx, app, question, source)
	return result.Answer
}

// CreateAndSavePlan forces Gemini to decompose a goal into JSON, then saves it to the Knowledge Graph.
func CreateAndSavePlan(ctx context.Context, app *infra.App, goal string) (string, error) {
	ctx = infra.WithApp(ctx, app)
	return agent.CreateAndSavePlan(ctx, goal)
}

// looksLikeQuestion checks if the input looks like a question or information request (for tests and SMS routing).
func looksLikeQuestion(input string) bool {
	input = strings.ToLower(strings.TrimSpace(input))
	if strings.HasSuffix(input, "?") {
		return true
	}
	questionPrefixes := []string{
		"what ", "what's ", "whats ", "where ", "where's ", "wheres ", "when ", "when's ", "whens ",
		"who ", "who's ", "whos ", "why ", "why's ", "whys ", "how ", "how's ", "hows ",
		"which ", "whose ", "is ", "are ", "was ", "were ", "will ", "would ", "could ", "should ", "can ",
		"do ", "does ", "did ", "tell me ", "show me ", "find ", "search ", "look up ", "lookup ",
		"list ", "describe ", "explain ",
	}
	for _, prefix := range questionPrefixes {
		if strings.HasPrefix(input, prefix) {
			return true
		}
	}
	return false
}
