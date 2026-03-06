package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

// ServiceEnv implements agent.FOHEnv, PlannerEnv, PrompterEnv, SpecialistsEnv, RollupEnv, and DreamerEnv
// by delegating to pkg/journal, pkg/memory, and pkg/infra (app from context).
type ServiceEnv struct{}

func (ServiceEnv) BuildSystemPrompt(ctx context.Context) string {
	return agent.BuildSystemPrompt(ctx, ServiceEnv{})
}

func (ServiceEnv) AddEntryAndEnqueue(ctx context.Context, content, source string, timestamp *string) (string, error) {
	entryUUID, err := journal.AddEntry(ctx, content, source, timestamp)
	if err != nil {
		return "", err
	}
	ts := time.Now().Format(time.RFC3339)
	if timestamp != nil && *timestamp != "" {
		ts = *timestamp
	}
	payload := map[string]interface{}{
		"uuid": entryUUID, "content": content, "timestamp": ts, "source": source,
	}
	app := infra.GetApp(ctx)
	if app != nil {
		if err := app.EnqueueTask(ctx, "/internal/process-entry", payload); err != nil {
			infra.LoggerFrom(ctx).Warn("failed to enqueue process-entry task, running inline", "entry_uuid", entryUUID, "error", err)
			app.SubmitAsync(func() {
				bgCtx := infra.WithApp(context.Background(), app)
				_ = ProcessEntry(bgCtx, entryUUID, content, ts, source)
			})
		}
	}
	return entryUUID, nil
}

func (ServiceEnv) EnqueueSaveQuery(ctx context.Context, question, answer, source string, isGap bool) error {
	app := infra.GetApp(ctx)
	if app == nil {
		return nil
	}
	return app.EnqueueTask(ctx, "/internal/save-query", map[string]interface{}{
		"question": question,
		"answer":   answer,
		"source":   source,
		"is_gap":   isGap,
	})
}

func (ServiceEnv) GetEntry(ctx context.Context, entryUUID string) (*journal.Entry, error) {
	return journal.GetEntry(ctx, entryUUID)
}

func (ServiceEnv) UpsertKnowledge(ctx context.Context, content, nodeType, metadata string, journalEntryIDs []string) (string, error) {
	return memory.UpsertKnowledge(ctx, content, nodeType, metadata, journalEntryIDs)
}

func (ServiceEnv) GetActiveContexts(ctx context.Context, limit int) ([]agent.ActiveContextItem, error) {
	nodes, metas, err := memory.GetActiveContexts(ctx, limit)
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

func (ServiceEnv) GetActiveSignals(ctx context.Context, limit int) (string, error) {
	return memory.GetActiveSignals(ctx, limit)
}

func (ServiceEnv) FindContextContent(ctx context.Context, name string) (string, error) {
	node, _, err := memory.FindContextByName(ctx, name)
	if err != nil || node == nil {
		return "", err
	}
	return node.Content, nil
}

func (ServiceEnv) UpsertSemanticMemory(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks, journalEntryIDs []string) (string, error) {
	return memory.UpsertSemanticMemory(ctx, content, nodeType, domain, significanceWeight, entityLinks, journalEntryIDs)
}

func (ServiceEnv) GetEntriesWithAnalysisForRollup(ctx context.Context, start, end string, limit int) (string, []string, error) {
	withAnalyses, err := journal.GetEntriesWithAnalysisByDateRange(ctx, start, end, limit)
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

func (ServiceEnv) GetWeeklySummariesForRollup(ctx context.Context, startDate, endDate string, limit int) (string, []string, error) {
	nodes, err := memory.GetWeeklySummaryNodesInRange(ctx, startDate, endDate, limit)
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

func (ServiceEnv) LoadDreamerInputs(ctx context.Context) (*agent.DreamerInputs, error) {
	cutoff := time.Now().Add(-24 * time.Hour)
	startDate := cutoff.Format("2006-01-02")
	endDate := time.Now().Format("2006-01-02")
	entries, err := journal.GetEntriesByDateRange(ctx, startDate, endDate, 200)
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
	if queries, qErr := journal.GetRecentQueries(ctx, 50); qErr == nil && len(queries) > 0 {
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

func (ServiceEnv) GenerateEmbedding(ctx context.Context, text string, taskType ...string) ([]float32, error) {
	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("no app config for embedding")
	}
	return infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, text, taskType...)
}

func (ServiceEnv) EnsureContextExists(ctx context.Context, name string) (string, error) {
	return memory.EnsureContextExists(ctx, name)
}

func (ServiceEnv) TouchContextBatch(ctx context.Context, contextUUID string, entryUUIDs []string, relevanceBoost float64) error {
	return memory.TouchContextBatch(ctx, contextUUID, entryUUIDs, relevanceBoost)
}

func (ServiceEnv) GetContextMetadata(ctx context.Context, contextUUID string) (*agent.ContextMetadata, error) {
	m, err := memory.GetContextMetadata(ctx, contextUUID)
	if err != nil || m == nil {
		return nil, err
	}
	return &agent.ContextMetadata{
		LastSynthesizedAt:           m.LastSynthesizedAt,
		SourceEntryCountAtSynthesis: m.SourceEntryCountAtSynthesis,
		SourceEntries:               m.SourceEntries,
		Relevance:                   m.Relevance,
	}, nil
}

func (ServiceEnv) TouchContext(ctx context.Context, contextUUID string, relevanceBoost float64) error {
	return memory.TouchContext(ctx, contextUUID, nil, relevanceBoost)
}

func (ServiceEnv) SynthesizeContext(ctx context.Context, contextUUID string) error {
	return memory.SynthesizeContext(ctx, contextUUID)
}

func (ServiceEnv) RunGapDetection(ctx context.Context, journalContext string, entryUUIDs []string) error {
	return RunGapDetection(ctx, journalContext, entryUUIDs)
}

func (ServiceEnv) RunProfileSynthesis(ctx context.Context, personaFacts []string) error {
	return RunProfileSynthesis(ctx, personaFacts)
}

func (ServiceEnv) RunEvolutionSynthesis(ctx context.Context, journalContext string) error {
	return RunEvolutionSynthesis(ctx, journalContext)
}
