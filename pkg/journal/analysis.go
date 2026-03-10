package journal

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
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

const (
	dateDisplayLen = 10
	maxTagLen      = 30
	maxTagWords    = 3
)

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

// journalAnalysisRaw is the LLM response shape: tags as tag_1..tag_5 to force a fixed number of slots (genai has no MaxItems).
type journalAnalysisRaw struct {
	Summary   string     `json:"summary"`
	Mood      string     `json:"mood"`
	Category  string     `json:"category"`
	Tag1      string     `json:"tag_1"`
	Tag2      string     `json:"tag_2"`
	Tag3      string     `json:"tag_3"`
	Tag4      string     `json:"tag_4"`
	Tag5      string     `json:"tag_5"`
	Entities  []Entity   `json:"entities"`
	OpenLoops []OpenLoop `json:"open_loops"`
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
			"tag_1":     {Type: genai.TypeString, Description: "Tag 1 of 5. 1-3 words, lowercase, max 30 chars. No sentences or reasoning."},
			"tag_2":     {Type: genai.TypeString, Description: "Tag 2 of 5. 1-3 words, lowercase, max 30 chars. No sentences or reasoning."},
			"tag_3":     {Type: genai.TypeString, Description: "Tag 3 of 5. 1-3 words, lowercase, max 30 chars. No sentences or reasoning."},
			"tag_4":     {Type: genai.TypeString, Description: "Tag 4 of 5. 1-3 words, lowercase, max 30 chars. Leave empty if not needed."},
			"tag_5":     {Type: genai.TypeString, Description: "Tag 5 of 5. 1-3 words, lowercase, max 30 chars. Leave empty if not needed."},
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
		Parts:          []*genai.Part{{Text: prompt}},
		Model:          app.Config().GeminiModel,
		GenConfig:      &infra.GenConfig{MaxOutputTokens: 1024, ResponseMIMEType: infra.MIMETypeJSON},
		ResponseSchema: schema,
	}
	resp, err := app.Dispatch(ctx, req)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("journal analysis failed", "error", err)
		return nil, fmt.Errorf("journal analysis: %w", err)
	}

	jsonText := infra.ExtractText(resp)
	raw, parseErr := llmjson.ParseLLMResponse[journalAnalysisRaw](jsonText, []string{"summary", "mood", "category", "tag_1", "tag_2", "tag_3", "tag_4", "tag_5", "entities", "open_loops"})
	if raw == nil {
		if parseErr == nil {
			parseErr = fmt.Errorf("parse failed")
		}
		infra.LoggerFrom(ctx).Warn("failed to parse journal analysis response", "error", parseErr)
		return nil, fmt.Errorf("journal analysis parse: %w", parseErr)
	}
	analysis := &JournalAnalysis{
		Summary:   raw.Summary,
		Mood:      raw.Mood,
		Category:  raw.Category,
		Entities:  raw.Entities,
		OpenLoops: raw.OpenLoops,
	}
	for _, t := range []string{raw.Tag1, raw.Tag2, raw.Tag3, raw.Tag4, raw.Tag5} {
		if cleaned := sanitizeTag(t); cleaned != "" {
			analysis.Tags = append(analysis.Tags, cleaned)
		}
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

// sanitizeTag returns a tag suitable for storage, or empty string to drop.
// Drops tags that are over maxTagLen, have more than maxTagWords, or contain sentence-like or reasoning content.
func sanitizeTag(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	if len(t) > maxTagLen {
		return ""
	}
	if strings.Contains(t, ". ") {
		return ""
	}
	// Drop meta-commentary / reasoning (e.g. "drop-off - status? not for gideon")
	if strings.Contains(t, "?") || strings.Contains(t, " - ") {
		return ""
	}
	words := strings.Fields(t)
	if len(words) > maxTagWords {
		return ""
	}
	return t
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
