package jot

import (
	"context"
	"strings"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/journal"
)

const (
	MaxIterations       = 10
	MaxMessagePairs     = 20
	ToolRepeatBackOffAt = 3
)

// QueryResult is the result of a query. Re-exported from agent.
type QueryResult = agent.QueryResult

// jotFOHEnv implements agent.FOHEnv by delegating to jot.
type jotFOHEnv struct{}

func (jotFOHEnv) BuildSystemPrompt(ctx context.Context) string {
	return buildSystemPrompt(ctx)
}

func (jotFOHEnv) AddEntryAndEnqueue(ctx context.Context, content, source string, timestamp *string) (string, error) {
	return AddEntry(ctx, content, source, timestamp)
}

func (jotFOHEnv) EnqueueSaveQuery(ctx context.Context, question, answer, source string, isGap bool) error {
	return EnqueueTask(ctx, "/internal/save-query", map[string]interface{}{
		"question": question,
		"answer":   answer,
		"source":   source,
		"is_gap":   isGap,
	})
}

func (jotFOHEnv) GetEntry(ctx context.Context, entryUUID string) (*journal.Entry, error) {
	return GetEntry(ctx, entryUUID)
}

// PlannerEnv: UpsertKnowledge delegates to jot.UpsertKnowledge.
func (jotFOHEnv) UpsertKnowledge(ctx context.Context, content, nodeType, metadata string, journalEntryIDs []string) (string, error) {
	return UpsertKnowledge(ctx, content, nodeType, metadata, journalEntryIDs)
}

// PrompterEnv: GetActiveContexts converts jot results to agent.ActiveContextItem.
func (jotFOHEnv) GetActiveContexts(ctx context.Context, limit int) ([]agent.ActiveContextItem, error) {
	nodes, metas, err := GetActiveContexts(ctx, limit)
	if err != nil || len(nodes) == 0 {
		return nil, err
	}
	out := make([]agent.ActiveContextItem, 0, len(nodes))
	for i, n := range nodes {
		if i >= len(metas) {
			break
		}
		out = append(out, agent.ActiveContextItem{
			ContextName: metas[i].ContextName,
			Relevance:   metas[i].Relevance,
			Content:     n.Content,
		})
	}
	return out, nil
}

// PrompterEnv: GetActiveSignals delegates to jot.GetActiveSignals.
func (jotFOHEnv) GetActiveSignals(ctx context.Context, limit int) (string, error) {
	return GetActiveSignals(ctx, limit)
}

// SpecialistsEnv: FindContextContent returns content of the named context.
func (jotFOHEnv) FindContextContent(ctx context.Context, name string) (string, error) {
	node, _, err := FindContextByName(ctx, name)
	if err != nil || node == nil {
		return "", err
	}
	return node.Content, nil
}

// SpecialistsEnv: UpsertSemanticMemory delegates to jot.UpsertSemanticMemory.
func (jotFOHEnv) UpsertSemanticMemory(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks, journalEntryIDs []string) (string, error) {
	return UpsertSemanticMemory(ctx, content, nodeType, domain, significanceWeight, entityLinks, journalEntryIDs)
}

// RunQuery runs a query against the journal using the agentic loop.
func RunQuery(ctx context.Context, question, source string) *QueryResult {
	return RunQueryWithDebug(ctx, question, source, true)
}

// RunQueryWithDebug runs a query with optional debug logging.
func RunQueryWithDebug(ctx context.Context, question, source string, debug bool) *QueryResult {
	return agent.RunQueryWithDebug(ctx, jotFOHEnv{}, question, source, debug)
}

// GetAnswer is a simple wrapper that returns just the answer string (for sync compatibility).
func GetAnswer(ctx context.Context, question, source string) string {
	result := RunQuery(ctx, question, source)
	return result.Answer
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
