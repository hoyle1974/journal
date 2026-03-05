package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/llmjson"
)

const (
	nodeTypeWeeklySummary  = "weekly_summary"
	nodeTypeMonthlySummary = "monthly_summary"
	rollUpSignificance     = 0.8
)

// rollUpOutput is the JSON output from the roll-up LLM.
type rollUpOutput struct {
	Summary         string   `json:"summary"`
	Themes          []string `json:"themes"`
	KeyEntities     []string `json:"key_entities"`
	OpenLoopsSummary string `json:"open_loops_summary"`
}

// lastCompletedWeekStartEnd returns the start and end dates (YYYY-MM-DD) of the last completed ISO week (Mon-Sun).
func lastCompletedWeekStartEnd(now time.Time) (start, end string) {
	daysSinceMonday := (int(now.Weekday()) + 6) % 7
	if now.Weekday() == time.Monday {
		daysSinceMonday = 0
	}
	thisMonday := now.AddDate(0, 0, -daysSinceMonday)
	lastWeekSunday := thisMonday.AddDate(0, 0, -1)
	lastWeekMonday := lastWeekSunday.AddDate(0, 0, -6)
	return lastWeekMonday.Format("2006-01-02"), lastWeekSunday.Format("2006-01-02")
}

// lastCompletedMonth returns the year and month of the last completed month.
func lastCompletedMonth(now time.Time) (year int, month int) {
	if now.Month() == 1 {
		return now.Year() - 1, 12
	}
	return now.Year(), int(now.Month()) - 1
}

// runRollUpLLM calls Gemini with the roll-up prompt and returns the content string to store (and parsed output for metadata if needed).
func runRollUpLLM(ctx context.Context, periodLabel, analysesText string) (string, *rollUpOutput, error) {
	client, err := GetGeminiClient(ctx)
	if err != nil {
		return "", nil, err
	}
	model := client.GenerativeModel(GetEffectiveModel(ctx, defaultConfig.DreamerModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(1024)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":           {Type: genai.TypeString},
			"themes":            {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"key_entities":      {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"open_loops_summary": {Type: genai.TypeString},
		},
	}
	userPrompt := prompts.FormatRollUp(periodLabel, WrapAsUserData(SanitizePrompt(analysesText)))

	apiCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	GeminiCallsTotal.Inc()
	resp, err := model.GenerateContent(apiCtx, genai.Text(userPrompt))
	if err != nil {
		return "", nil, WrapLLMError(err)
	}
	text := extractTextFromResponse(resp)
	var out rollUpOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		if err := llmjson.RepairAndUnmarshal(text, &out); err != nil {
			return "", nil, fmt.Errorf("roll-up parse: %w", err)
		}
	}
	content := text
	if out.Summary != "" {
		content = out.Summary
		if len(out.Themes) > 0 {
			content += "\nThemes: " + strings.Join(out.Themes, ", ")
		}
		if len(out.KeyEntities) > 0 {
			content += "\nKey entities: " + strings.Join(out.KeyEntities, ", ")
		}
		if out.OpenLoopsSummary != "" {
			content += "\nOpen loops: " + out.OpenLoopsSummary
		}
	}
	return content, &out, nil
}

// RunWeeklyRollup synthesizes the last completed week's journal analyses into a weekly_summary knowledge node.
func RunWeeklyRollup(ctx context.Context) (int, error) {
	ctx, span := StartSpan(ctx, "cron.weekly_rollup")
	defer span.End()

	start, end := lastCompletedWeekStartEnd(time.Now())
	periodLabel := fmt.Sprintf("Week of %s", start)

	withAnalyses, err := GetEntriesWithAnalysisByDateRange(ctx, start, end, 500)
	if err != nil {
		return 0, fmt.Errorf("weekly rollup get entries: %w", err)
	}
	if len(withAnalyses) == 0 {
		LoggerFrom(ctx).Info("weekly rollup: no entries in period", "start", start, "end", end)
		return 0, nil
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
	analysesText := strings.Join(lines, "\n")
	if len(analysesText) > 6000 {
		analysesText = truncateToMaxBytes(analysesText, 6000) + "\n... (truncated)"
	}

	content, _, err := runRollUpLLM(ctx, periodLabel, analysesText)
	if err != nil {
		return 0, err
	}

	_, err = UpsertSemanticMemory(ctx, content, nodeTypeWeeklySummary, "thought", rollUpSignificance, nil, sourceIDs)
	if err != nil {
		return 0, fmt.Errorf("weekly rollup upsert: %w", err)
	}
	LoggerFrom(ctx).Info("weekly rollup wrote summary", "period", periodLabel, "entries", len(sourceIDs))
	span.SetAttributes(map[string]string{"period_start": start, "period_end": end, "entries": fmt.Sprintf("%d", len(sourceIDs))})
	return len(sourceIDs), nil
}

// GetWeeklySummaryNodesInRange returns knowledge nodes of type weekly_summary whose timestamp falls in [startDate, endDate] (YYYY-MM-DD).
func GetWeeklySummaryNodesInRange(ctx context.Context, startDate, endDate string, limit int) ([]KnowledgeNode, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", nodeTypeWeeklySummary).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Asc).
		Limit(limit)
	nodes, err := QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		data := doc.Data()
		return KnowledgeNode{
			UUID:            doc.Ref.ID,
			Content:         getStringField(data, "content"),
			NodeType:        getStringField(data, "node_type"),
			Metadata:        getStringField(data, "metadata"),
			Timestamp:       getStringField(data, "timestamp"),
			JournalEntryIDs: getStringSliceField(data, "journal_entry_ids"),
		}, nil
	})
	if err != nil {
		return nil, WrapFirestoreIndexError(err)
	}
	return nodes, nil
}

// RunMonthlyRollup synthesizes the last completed month's weekly summaries into a monthly_summary knowledge node.
func RunMonthlyRollup(ctx context.Context) (int, error) {
	ctx, span := StartSpan(ctx, "cron.monthly_rollup")
	defer span.End()

	now := time.Now()
	year, month := lastCompletedMonth(now)
	startDate := fmt.Sprintf("%04d-%02d-01", year, month)
	lastDay := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, now.Location()).AddDate(0, 1, -1).Day()
	endDate := fmt.Sprintf("%04d-%02d-%02d", year, month, lastDay)
	periodLabel := fmt.Sprintf("%04d-%02d", year, month)

	weeklyNodes, err := GetWeeklySummaryNodesInRange(ctx, startDate, endDate, 10)
	if err != nil {
		return 0, fmt.Errorf("monthly rollup get weekly nodes: %w", err)
	}
	if len(weeklyNodes) == 0 {
		LoggerFrom(ctx).Info("monthly rollup: no weekly summaries in period", "period", periodLabel)
		return 0, nil
	}

	var lines []string
	var allSourceIDs []string
	seenIDs := make(map[string]bool)
	for _, n := range weeklyNodes {
		lines = append(lines, n.Content)
		for _, id := range n.JournalEntryIDs {
			if !seenIDs[id] {
				seenIDs[id] = true
				allSourceIDs = append(allSourceIDs, id)
			}
		}
	}
	analysesText := strings.Join(lines, "\n\n")
	if len(analysesText) > 6000 {
		analysesText = truncateToMaxBytes(analysesText, 6000) + "\n... (truncated)"
	}

	content, _, err := runRollUpLLM(ctx, "Month "+periodLabel, analysesText)
	if err != nil {
		return 0, err
	}

	_, err = UpsertSemanticMemory(ctx, content, nodeTypeMonthlySummary, "thought", rollUpSignificance, nil, allSourceIDs)
	if err != nil {
		return 0, fmt.Errorf("monthly rollup upsert: %w", err)
	}
	LoggerFrom(ctx).Info("monthly rollup wrote summary", "period", periodLabel, "weekly_nodes", len(weeklyNodes))
	span.SetAttributes(map[string]string{"period": periodLabel, "weekly_nodes": fmt.Sprintf("%d", len(weeklyNodes))})
	return len(weeklyNodes), nil
}
