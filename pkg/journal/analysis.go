package journal

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/llmjson"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

const (
	EntityStatusPlanned    = "Planned"
	EntityStatusInProgress = "In-Progress"
	EntityStatusStalled    = "Stalled"
	EntityStatusCompleted  = "Completed"
)

const dateDisplayLen = 10

// Entity represents a person, project, or event mentioned in a journal entry.
type Entity struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	SourceID string `json:"source_id"`
}

// OpenLoop represents a task or unanswered question from a journal entry.
type OpenLoop struct {
	Task     string `json:"task"`
	Priority string `json:"priority"`
	SourceID string `json:"source_id"`
}

// JournalAnalysis is the structured output from analyzing a journal entry.
type JournalAnalysis struct {
	Summary   string     `json:"summary"`
	Mood      string     `json:"mood"`
	Category  string     `json:"category"` // work, personal, health, finance, logistics
	Tags      []string   `json:"tags"`
	Entities  []Entity   `json:"entities"`
	OpenLoops []OpenLoop `json:"open_loops"`
	SourceID  string     `json:"source_id"`
}

// AnalyzeJournalEntry uses Gemini with JSON schema to analyze a journal entry.
func AnalyzeJournalEntry(ctx context.Context, entryContent, entryUUID, entryTimestamp string) (*JournalAnalysis, error) {
	ctx, span := infra.StartSpan(ctx, "journal.analyze")
	defer span.End()

	if len(entryContent) < 20 {
		return nil, nil
	}

	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		infra.LoggerFrom(ctx).Warn("journal analysis skipped", "reason", "no app or config")
		return nil, fmt.Errorf("no app or config")
	}

	entryDate := entryTimestamp
	if len(entryDate) > dateDisplayLen {
		entryDate = utils.TruncateString(entryDate, dateDisplayLen)
	}
	if entryDate == "" {
		entryDate = "unknown"
	}

	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":   {Type: genai.TypeString},
			"mood":      {Type: genai.TypeString},
			"category":  {Type: genai.TypeString, Enum: []string{"work", "personal", "health", "finance", "logistics"}},
			"tags":      {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"open_loops": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"task":      {Type: genai.TypeString},
						"priority":  {Type: genai.TypeString},
						"source_id": {Type: genai.TypeString},
					},
				},
			},
			"entities": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"name":      {Type: genai.TypeString},
						"type":      {Type: genai.TypeString},
						"status":    {Type: genai.TypeString},
						"source_id": {Type: genai.TypeString},
					},
				},
			},
		},
	}
	prompt := prompts.FormatJournalAnalyze(entryUUID, entryDate, utils.WrapAsUserData(utils.SanitizePrompt(entryContent)))
	req := &infra.LLMRequest{
		Parts:           []genai.Part{genai.Text(prompt)},
		Model:           app.Config().GeminiModel,
		GenConfig:       &infra.GenConfig{MaxOutputTokens: 1024, ResponseMIMEType: "application/json"},
		ResponseSchema:  schema,
	}
	resp, err := app.Dispatch(ctx, req)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("journal analysis failed", "error", err)
		return nil, fmt.Errorf("journal analysis: %w", err)
	}

	jsonText := infra.ExtractText(resp)
	analysis, parseErr := llmjson.ParseLLMResponse[JournalAnalysis](jsonText, []string{"summary", "mood", "category", "tags", "entities", "open_loops"})
	if analysis == nil {
		if parseErr == nil {
			parseErr = fmt.Errorf("parse failed")
		}
		infra.LoggerFrom(ctx).Warn("failed to parse journal analysis response", "error", parseErr)
		return nil, fmt.Errorf("journal analysis parse: %w", parseErr)
	}
	for i := range analysis.Entities {
		if analysis.Entities[i].SourceID == "" {
			analysis.Entities[i].SourceID = entryUUID
		}
		analysis.Entities[i].Status = NormalizeEntityStatus(analysis.Entities[i].Status)
	}
	for j := range analysis.OpenLoops {
		if analysis.OpenLoops[j].SourceID == "" {
			analysis.OpenLoops[j].SourceID = entryUUID
		}
	}
	analysis.SourceID = entryUUID
	return analysis, nil
}

// NormalizeEntityStatus maps LLM output to canonical status.
func NormalizeEntityStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "planned", "planning", "pending", "scheduled":
		return EntityStatusPlanned
	case "in-progress", "in progress", "ongoing", "active", "started":
		return EntityStatusInProgress
	case "stalled", "blocked", "on hold", "paused":
		return EntityStatusStalled
	case "completed", "done", "finished", "resolved":
		return EntityStatusCompleted
	default:
		if s == "" {
			return EntityStatusInProgress
		}
		return s
	}
}
