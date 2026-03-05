package jot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/utils"
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
	return agent.BuildSystemPrompt(ctx, jotFOHEnv{})
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

// RollupEnv: GetEntriesWithAnalysisForRollup formats entries with analysis for the rollup LLM.
func (jotFOHEnv) GetEntriesWithAnalysisForRollup(ctx context.Context, start, end string, limit int) (string, []string, error) {
	withAnalyses, err := GetEntriesWithAnalysisByDateRange(ctx, start, end, limit)
	if err != nil {
		return "", nil, err
	}
	var lines []string
	var sourceIDs []string
	for _, ew := range withAnalyses {
		sourceIDs = append(sourceIDs, ew.Entry.UUID)
		if ew.Analysis != nil {
			lines = append(lines, fmt.Sprintf("Summary: %s | Mood: %s | Tags: %s",
				ew.Analysis.Summary, ew.Analysis.Mood, strings.Join(ew.Analysis.Tags, ", ")))
			for _, e := range ew.Analysis.Entities {
				lines = append(lines, fmt.Sprintf("  Entity: %s (%s) %s", e.Name, e.Type, e.Status))
			}
			for _, o := range ew.Analysis.OpenLoops {
				lines = append(lines, fmt.Sprintf("  Open loop: %s [%s]", o.Task, o.Priority))
			}
		}
	}
	return strings.Join(lines, "\n"), sourceIDs, nil
}

// RollupEnv: GetWeeklySummariesForRollup returns concatenated weekly summary content and aggregated source IDs.
func (jotFOHEnv) GetWeeklySummariesForRollup(ctx context.Context, startDate, endDate string, limit int) (string, []string, error) {
	nodes, err := GetWeeklySummaryNodesInRange(ctx, startDate, endDate, limit)
	if err != nil {
		return "", nil, err
	}
	var lines []string
	var allSourceIDs []string
	seenIDs := make(map[string]bool)
	for _, n := range nodes {
		lines = append(lines, n.Content)
		for _, id := range n.JournalEntryIDs {
			if !seenIDs[id] {
				seenIDs[id] = true
				allSourceIDs = append(allSourceIDs, id)
			}
		}
	}
	return strings.Join(lines, "\n\n"), allSourceIDs, nil
}

// DreamerResult is the outcome of a dream run. Re-exported from agent.
type DreamerResult = agent.DreamerResult

// DreamerEnv: LoadDreamerInputs loads the last 24h of entries and builds journal context, entry UUIDs, and recent queries text.
func (jotFOHEnv) LoadDreamerInputs(ctx context.Context) (*agent.DreamerInputs, error) {
	cutoff := time.Now().Add(-24 * time.Hour)
	startDate := cutoff.Format("2006-01-02")
	endDate := time.Now().Format("2006-01-02")
	entries, err := GetEntriesByDateRange(ctx, startDate, endDate, 200)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return &agent.DreamerInputs{}, nil
	}
	var lines []string
	for _, e := range entries {
		lines = append(lines, fmt.Sprintf("[%s] %s", e.Timestamp, e.Content))
	}
	journalContext := strings.Join(lines, "\n")
	if len(journalContext) > 6000 {
		journalContext = utils.TruncateToMaxBytes(journalContext, 6000) + "\n... (truncated)"
	}
	entryUUIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		entryUUIDs = append(entryUUIDs, e.UUID)
	}
	recentQueriesText := ""
	if queries, qErr := GetRecentQueries(ctx, 50); qErr == nil && len(queries) > 0 {
		var qLines []string
		for _, q := range queries {
			ts := q.Timestamp
			if len(ts) > 16 {
				ts = ts[:16]
			}
			qLines = append(qLines, fmt.Sprintf("[%s] Q: %s\n  A: %s", ts, q.Question, utils.TruncateString(q.Answer, 200)))
		}
		recentQueriesText = strings.Join(qLines, "\n\n")
		if len(recentQueriesText) > 8000 {
			recentQueriesText = utils.TruncateToMaxBytes(recentQueriesText, 8000) + "\n... (truncated)"
		}
	}
	return &agent.DreamerInputs{
		JournalContext:    journalContext,
		EntryUUIDs:        entryUUIDs,
		RecentQueriesText: recentQueriesText,
	}, nil
}

// DreamerEnv: GenerateEmbedding delegates to jot.GenerateEmbedding.
func (jotFOHEnv) GenerateEmbedding(ctx context.Context, text string, taskType ...string) ([]float32, error) {
	return GenerateEmbedding(ctx, text, taskType...)
}

// DreamerEnv: EnsureContextExists delegates to jot.EnsureContextExists.
func (jotFOHEnv) EnsureContextExists(ctx context.Context, name string) (string, error) {
	return EnsureContextExists(ctx, name)
}

// DreamerEnv: TouchContextBatch delegates to jot.TouchContextBatch.
func (jotFOHEnv) TouchContextBatch(ctx context.Context, contextUUID string, entryUUIDs []string, relevanceBoost float64) error {
	return TouchContextBatch(ctx, contextUUID, entryUUIDs, relevanceBoost)
}

// DreamerEnv: GetContextMetadata converts jot metadata to agent.ContextMetadata.
func (jotFOHEnv) GetContextMetadata(ctx context.Context, contextUUID string) (*agent.ContextMetadata, error) {
	m, err := GetContextMetadata(ctx, contextUUID)
	if err != nil || m == nil {
		return nil, err
	}
	return &agent.ContextMetadata{
		LastSynthesizedAt:          m.LastSynthesizedAt,
		SourceEntryCountAtSynthesis: m.SourceEntryCountAtSynthesis,
		SourceEntries:              m.SourceEntries,
		Relevance:                  m.Relevance,
	}, nil
}

// DreamerEnv: TouchContext delegates to jot.TouchContext (no new source entry).
func (jotFOHEnv) TouchContext(ctx context.Context, contextUUID string, relevanceBoost float64) error {
	return TouchContext(ctx, contextUUID, nil, relevanceBoost)
}

// DreamerEnv: SynthesizeContext delegates to jot.SynthesizeContext.
func (jotFOHEnv) SynthesizeContext(ctx context.Context, contextUUID string) error {
	return SynthesizeContext(ctx, contextUUID)
}

// DreamerEnv: RunGapDetection delegates to jot.RunGapDetection.
func (jotFOHEnv) RunGapDetection(ctx context.Context, journalContext string, entryUUIDs []string) error {
	return RunGapDetection(ctx, journalContext, entryUUIDs)
}

// DreamerEnv: RunProfileSynthesis delegates to jot.RunProfileSynthesis.
func (jotFOHEnv) RunProfileSynthesis(ctx context.Context, personaFacts []string) error {
	return RunProfileSynthesis(ctx, personaFacts)
}

// DreamerEnv: RunEvolutionSynthesis delegates to jot.RunEvolutionSynthesis.
func (jotFOHEnv) RunEvolutionSynthesis(ctx context.Context, journalContext string) error {
	return RunEvolutionSynthesis(ctx, journalContext)
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

// CreateAndSavePlan forces Gemini to decompose a goal into JSON, then saves it to the Knowledge Graph.
func CreateAndSavePlan(ctx context.Context, goal string) (string, error) {
	return agent.CreateAndSavePlan(ctx, jotFOHEnv{}, goal)
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
