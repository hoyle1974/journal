package service

import (
	"context"
	"strings"

	"github.com/jackstrohm/jot/pkg/agent"
)

const (
	MaxIterations       = 10
	MaxMessagePairs     = 20
	ToolRepeatBackOffAt = 3
)

// QueryResult is the result of a query. Re-exported from agent for callers.
type QueryResult = agent.QueryResult

// RunQuery runs a query against the journal using the agentic loop.
func RunQuery(ctx context.Context, question, source string) *QueryResult {
	return RunQueryWithDebug(ctx, question, source, true)
}

// RunQueryWithDebug runs a query with optional debug logging.
func RunQueryWithDebug(ctx context.Context, question, source string, debug bool) *QueryResult {
	return agent.RunQueryWithDebug(ctx, ServiceEnv{}, question, source, debug)
}

// GetAnswer returns just the answer string (for sync compatibility).
func GetAnswer(ctx context.Context, question, source string) string {
	result := RunQuery(ctx, question, source)
	return result.Answer
}

// CreateAndSavePlan forces Gemini to decompose a goal into JSON, then saves it to the Knowledge Graph.
func CreateAndSavePlan(ctx context.Context, goal string) (string, error) {
	return agent.CreateAndSavePlan(ctx, ServiceEnv{}, goal)
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
