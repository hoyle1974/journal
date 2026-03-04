package jot

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/llmjson"
)

// Entity represents a person, project, or event mentioned in a journal entry.
type Entity struct {
	Name   string `json:"name"`
	Type   string `json:"type"`   // e.g. "person", "project", "event"
	Status string `json:"status"` // e.g. "ongoing", "planned", "resolved"
}

// JournalAnalysis is the structured output from analyzing a journal entry.
type JournalAnalysis struct {
	Summary   string   `json:"summary"`
	Mood      string   `json:"mood"`
	Tags      []string `json:"tags"`
	Entities  []Entity `json:"entities"`
	OpenLoops []string `json:"open_loops"` // tasks or unanswered questions
	SourceID  string   `json:"source_id"`  // entry UUID; set by caller after unmarshal
}

// AnalyzeJournalEntry uses Gemini with JSON schema to analyze a journal entry.
// Returns a JournalAnalysis with SourceID set to entryUUID. Caller may persist it on the entry doc.
func AnalyzeJournalEntry(ctx context.Context, entryContent, entryUUID string) (*JournalAnalysis, error) {
	ctx, span := StartSpan(ctx, "journal.analyze")
	defer span.End()

	if len(entryContent) < 20 {
		return nil, nil
	}

	client, err := GetGeminiClient(ctx)
	if err != nil {
		LoggerFrom(ctx).Warn("journal analysis skipped", "reason", "no gemini client", "error", err)
		return nil, err
	}

	model := client.GenerativeModel(GetEffectiveModel(ctx, GeminiModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(1024)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":   {Type: genai.TypeString},
			"mood":      {Type: genai.TypeString},
			"tags":      {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"open_loops": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"entities": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"name":   {Type: genai.TypeString},
						"type":   {Type: genai.TypeString},
						"status": {Type: genai.TypeString},
					},
				},
			},
		},
	}

	prompt := prompts.FormatJournalAnalyze(WrapAsUserData(SanitizePrompt(entryContent)))
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		LoggerFrom(ctx).Warn("journal analysis failed", "error", err)
		return nil, fmt.Errorf("journal analysis: %w", err)
	}

	jsonText := extractTextFromResponse(resp)
	var analysis JournalAnalysis
	if err := json.Unmarshal([]byte(jsonText), &analysis); err != nil {
		if err := llmjson.RepairAndUnmarshal(jsonText, &analysis); err != nil {
			partial, _ := llmjson.PartialUnmarshalObject(jsonText, []string{"summary", "mood", "tags", "entities", "open_loops"})
			if len(partial) > 0 {
				if raw, ok := partial["summary"]; ok && len(raw) > 0 {
					_ = json.Unmarshal(raw, &analysis.Summary)
				}
				if raw, ok := partial["mood"]; ok && len(raw) > 0 {
					_ = json.Unmarshal(raw, &analysis.Mood)
				}
				if raw, ok := partial["tags"]; ok && len(raw) > 0 {
					_ = json.Unmarshal(raw, &analysis.Tags)
				}
				if raw, ok := partial["open_loops"]; ok && len(raw) > 0 {
					_ = json.Unmarshal(raw, &analysis.OpenLoops)
				}
				if raw, ok := partial["entities"]; ok && len(raw) > 0 {
					_ = json.Unmarshal(raw, &analysis.Entities)
				}
			}
			if analysis.Summary == "" && analysis.Mood == "" && len(analysis.Tags) == 0 && len(analysis.Entities) == 0 && len(analysis.OpenLoops) == 0 {
				LoggerFrom(ctx).Warn("failed to parse journal analysis response", "error", err)
				return nil, fmt.Errorf("journal analysis parse: %w", err)
			}
		}
	}
	analysis.SourceID = entryUUID
	return &analysis, nil
}
