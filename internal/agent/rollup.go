package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

const (
	NodeTypeWeeklySummary  = "weekly_summary"
	NodeTypeMonthlySummary = "monthly_summary"
	RollUpSignificance     = 0.8
)

type rollUpOutput struct {
	Summary          string   `json:"summary"`
	Themes           []string `json:"themes"`
	KeyEntities      []string `json:"key_entities"`
	OpenLoopsSummary string   `json:"open_loops_summary"`
}

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

func lastCompletedMonth(now time.Time) (year int, month int) {
	if now.Month() == 1 {
		return now.Year() - 1, 12
	}
	return now.Year(), int(now.Month()) - 1
}

func runRollUpLLM(ctx context.Context, app *infra.App, periodLabel, analysesText string) (string, *rollUpOutput, error) {
	if app == nil {
		return "", nil, fmt.Errorf("no app in context")
	}
	userPrompt, err := prompts.BuildRollUp(prompts.RollUpData{
		PeriodLabel:  periodLabel,
		AnalysesText: utils.WrapAsUserData(utils.SanitizePrompt(analysesText)),
	})
	if err != nil {
		return "", nil, fmt.Errorf("build roll up prompt: %w", err)
	}
	req := &infra.LLMRequest{
		Parts:     []*genai.Part{{Text: userPrompt}},
		Model:     app.Config().DreamerModel,
		GenConfig: &infra.GenConfig{MaxOutputTokens: 1024},
	}
	apiCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	infra.GeminiCallsTotal.Inc()
	resp, err := app.Dispatch(apiCtx, req)
	if err != nil {
		return "", nil, infra.WrapLLMError(err)
	}
	text := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	simple, sections := utils.ParseKeyValueMap(text)
	out := &rollUpOutput{
		Summary:          simple["summary"],
		Themes:           sections["themes"],
		KeyEntities:      sections["key_entities"],
		OpenLoopsSummary: simple["open_loops_summary"],
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
	return content, out, nil
}

func getEntriesWithAnalysisForRollup(ctx context.Context, start, end string, limit int) (analysesText string, sourceIDs []string, err error) {
	withAnalyses, err := journal.GetEntriesWithAnalysisByDateRange(ctx, start, end, limit)
	if err != nil {
		return "", nil, err
	}
	var lines []string
	var ids []string
	for _, ew := range withAnalyses {
		ids = append(ids, ew.Entry.UUID)
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
	return strings.Join(lines, "\n"), ids, nil
}

func getWeeklySummariesForRollup(ctx context.Context, startDate, endDate string, limit int) (contentText string, sourceIDs []string, err error) {
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

// RunWeeklyRollup synthesizes the last completed week's journal analyses into a weekly_summary knowledge node.
func RunWeeklyRollup(ctx context.Context) (int, error) {
	ctx, span := infra.StartSpan(ctx, "cron.weekly_rollup")
	defer span.End()

	app := infra.GetApp(ctx)
	start, end := lastCompletedWeekStartEnd(time.Now())
	periodLabel := fmt.Sprintf("Week of %s", start)

	analysesText, sourceIDs, err := getEntriesWithAnalysisForRollup(ctx, start, end, 500)
	if err != nil {
		return 0, fmt.Errorf("weekly rollup get entries: %w", err)
	}
	if analysesText == "" && len(sourceIDs) == 0 {
		infra.LoggerFrom(ctx).Info("weekly rollup: no entries in period", "start", start, "end", end)
		return 0, nil
	}
	if len(analysesText) > 6000 {
		analysesText = utils.TruncateToMaxBytes(analysesText, 6000) + "\n... (truncated)"
	}

	content, _, err := runRollUpLLM(ctx, app, periodLabel, analysesText)
	if err != nil {
		return 0, err
	}

	_, err = memory.UpsertSemanticMemory(ctx, content, NodeTypeWeeklySummary, "thought", RollUpSignificance, nil, sourceIDs)
	if err != nil {
		return 0, fmt.Errorf("weekly rollup upsert: %w", err)
	}
	infra.LoggerFrom(ctx).Info("weekly rollup wrote summary", "period", periodLabel, "entries", len(sourceIDs))
	span.SetAttributes(map[string]string{"period_start": start, "period_end": end, "entries": fmt.Sprintf("%d", len(sourceIDs))})
	return len(sourceIDs), nil
}

// RunMonthlyRollup synthesizes the last completed month's weekly summaries into a monthly_summary knowledge node.
func RunMonthlyRollup(ctx context.Context) (int, error) {
	ctx, span := infra.StartSpan(ctx, "cron.monthly_rollup")
	defer span.End()

	app := infra.GetApp(ctx)
	now := time.Now()
	year, month := lastCompletedMonth(now)
	startDate := fmt.Sprintf("%04d-%02d-01", year, month)
	lastDay := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, now.Location()).AddDate(0, 1, -1).Day()
	endDate := fmt.Sprintf("%04d-%02d-%02d", year, month, lastDay)
	periodLabel := fmt.Sprintf("%04d-%02d", year, month)

	contentText, allSourceIDs, err := getWeeklySummariesForRollup(ctx, startDate, endDate, 10)
	if err != nil {
		return 0, fmt.Errorf("monthly rollup get weekly nodes: %w", err)
	}
	if contentText == "" && len(allSourceIDs) == 0 {
		infra.LoggerFrom(ctx).Info("monthly rollup: no weekly summaries in period", "period", periodLabel)
		return 0, nil
	}
	if len(contentText) > 6000 {
		contentText = utils.TruncateToMaxBytes(contentText, 6000) + "\n... (truncated)"
	}

	content, _, err := runRollUpLLM(ctx, app, "Month "+periodLabel, contentText)
	if err != nil {
		return 0, err
	}

	_, err = memory.UpsertSemanticMemory(ctx, content, NodeTypeMonthlySummary, "thought", RollUpSignificance, nil, allSourceIDs)
	if err != nil {
		return 0, fmt.Errorf("monthly rollup upsert: %w", err)
	}
	infra.LoggerFrom(ctx).Info("monthly rollup wrote summary", "period", periodLabel)
	span.SetAttributes(map[string]string{"period": periodLabel})
	return len(allSourceIDs), nil
}
