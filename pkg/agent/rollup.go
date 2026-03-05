package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/llmjson"
	"github.com/jackstrohm/jot/pkg/infra"
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

func runRollUpLLM(ctx context.Context, periodLabel, analysesText string) (string, *rollUpOutput, error) {
	app := infra.GetApp(ctx)
	if app == nil {
		return "", nil, fmt.Errorf("no app in context")
	}
	client, err := app.Gemini(ctx)
	if err != nil {
		return "", nil, err
	}
	model := client.GenerativeModel(app.DreamerModel())
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(1024)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":            {Type: genai.TypeString},
			"themes":             {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"key_entities":       {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"open_loops_summary": {Type: genai.TypeString},
		},
	}
	userPrompt := prompts.FormatRollUp(periodLabel, utils.WrapAsUserData(utils.SanitizePrompt(analysesText)))

	apiCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	infra.GeminiCallsTotal.Inc()
	resp, err := model.GenerateContent(apiCtx, genai.Text(userPrompt))
	if err != nil {
		return "", nil, infra.WrapLLMError(err)
	}
	text := infra.ExtractTextFromResponse(resp)
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
func RunWeeklyRollup(ctx context.Context, env RollupEnv) (int, error) {
	ctx, span := infra.StartSpan(ctx, "cron.weekly_rollup")
	defer span.End()

	start, end := lastCompletedWeekStartEnd(time.Now())
	periodLabel := fmt.Sprintf("Week of %s", start)

	analysesText, sourceIDs, err := env.GetEntriesWithAnalysisForRollup(ctx, start, end, 500)
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

	content, _, err := runRollUpLLM(ctx, periodLabel, analysesText)
	if err != nil {
		return 0, err
	}

	_, err = env.UpsertSemanticMemory(ctx, content, NodeTypeWeeklySummary, "thought", RollUpSignificance, nil, sourceIDs)
	if err != nil {
		return 0, fmt.Errorf("weekly rollup upsert: %w", err)
	}
	infra.LoggerFrom(ctx).Info("weekly rollup wrote summary", "period", periodLabel, "entries", len(sourceIDs))
	span.SetAttributes(map[string]string{"period_start": start, "period_end": end, "entries": fmt.Sprintf("%d", len(sourceIDs))})
	return len(sourceIDs), nil
}

// RunMonthlyRollup synthesizes the last completed month's weekly summaries into a monthly_summary knowledge node.
func RunMonthlyRollup(ctx context.Context, env RollupEnv) (int, error) {
	ctx, span := infra.StartSpan(ctx, "cron.monthly_rollup")
	defer span.End()

	now := time.Now()
	year, month := lastCompletedMonth(now)
	startDate := fmt.Sprintf("%04d-%02d-01", year, month)
	lastDay := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, now.Location()).AddDate(0, 1, -1).Day()
	endDate := fmt.Sprintf("%04d-%02d-%02d", year, month, lastDay)
	periodLabel := fmt.Sprintf("%04d-%02d", year, month)

	contentText, allSourceIDs, err := env.GetWeeklySummariesForRollup(ctx, startDate, endDate, 10)
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

	content, _, err := runRollUpLLM(ctx, "Month "+periodLabel, contentText)
	if err != nil {
		return 0, err
	}

	_, err = env.UpsertSemanticMemory(ctx, content, NodeTypeMonthlySummary, "thought", RollUpSignificance, nil, allSourceIDs)
	if err != nil {
		return 0, fmt.Errorf("monthly rollup upsert: %w", err)
	}
	infra.LoggerFrom(ctx).Info("monthly rollup wrote summary", "period", periodLabel)
	span.SetAttributes(map[string]string{"period": periodLabel})
	return len(allSourceIDs), nil
}
