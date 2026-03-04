package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/llmjson"
)

// Canonical entity status values for queryability (e.g. filter Event "Party" where Status != Completed).
const (
	EntityStatusPlanned    = "Planned"
	EntityStatusInProgress = "In-Progress"
	EntityStatusStalled    = "Stalled"
	EntityStatusCompleted  = "Completed"
)

// Entity represents a person, project, or event mentioned in a journal entry.
// Status must be one of: Planned, In-Progress, Stalled, Completed (normalized after parse if needed).
type Entity struct {
	Name     string `json:"name"`
	Type     string `json:"type"`     // "person", "project", "event", "place"
	Status   string `json:"status"`   // canonical: Planned, In-Progress, Stalled, Completed
	SourceID string `json:"source_id"` // entry UUID; required for traceability
}

// OpenLoop represents a task or unanswered question from a journal entry.
type OpenLoop struct {
	Task     string `json:"task"`
	Priority string `json:"priority"` // e.g. "low", "med", "high"
	SourceID string `json:"source_id"` // entry UUID; required for traceability
}

// JournalAnalysis is the structured output from analyzing a journal entry.
type JournalAnalysis struct {
	Summary   string     `json:"summary"`
	Mood      string     `json:"mood"`
	Tags      []string   `json:"tags"`
	Entities  []Entity   `json:"entities"`
	OpenLoops []OpenLoop `json:"open_loops"` // tasks or unanswered questions
	SourceID  string     `json:"source_id"`  // entry UUID; set by caller after unmarshal
}

// AnalyzeJournalEntry uses Gemini with JSON schema to analyze a journal entry.
// entryTimestamp is optional (e.g. RFC3339); if empty, date context may be omitted.
// Returns a JournalAnalysis with SourceID set on the analysis and every entity/open_loop (guardrail fills if LLM omits).
func AnalyzeJournalEntry(ctx context.Context, entryContent, entryUUID, entryTimestamp string) (*JournalAnalysis, error) {
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

	entryDate := TruncateTimestamp(entryTimestamp, DateDisplayLen)
	if entryDate == "" {
		entryDate = "unknown"
	}

	model := client.GenerativeModel(GetEffectiveModel(ctx, GeminiModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(1024)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary": {Type: genai.TypeString},
			"mood":    {Type: genai.TypeString},
			"tags":    {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
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

	prompt := prompts.FormatJournalAnalyze(entryUUID, entryDate, WrapAsUserData(SanitizePrompt(entryContent)))
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		LoggerFrom(ctx).Warn("journal analysis failed", "error", err)
		return nil, fmt.Errorf("journal analysis: %w", err)
	}

	jsonText := extractTextFromResponse(resp)
	analysis, parseErr := llmjson.ParseLLMResponse[JournalAnalysis](jsonText, []string{"summary", "mood", "tags", "entities", "open_loops"})
	if analysis == nil {
		if parseErr == nil {
			parseErr = fmt.Errorf("parse failed")
		}
		LoggerFrom(ctx).Warn("failed to parse journal analysis response", "error", parseErr)
		return nil, fmt.Errorf("journal analysis parse: %w", parseErr)
	}
	// Consistency guardrail: ensure every entity and open_loop has source_id; normalize entity status
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

// NormalizeEntityStatus maps LLM output to canonical status (Planned, In-Progress, Stalled, Completed).
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
			return EntityStatusInProgress // default for unspecified
		}
		return s // preserve if already canonical or unknown
	}
}

// parseOpenLoops unmarshals open_loops from raw JSON, supporting either []OpenLoop or legacy []string.
func parseOpenLoops(raw []byte, out *[]OpenLoop) error {
	var asStructs []OpenLoop
	if err := json.Unmarshal(raw, &asStructs); err == nil {
		*out = asStructs
		return nil
	}
	var asStrings []string
	if err := json.Unmarshal(raw, &asStrings); err != nil {
		return err
	}
	*out = make([]OpenLoop, len(asStrings))
	for i, s := range asStrings {
		(*out)[i] = OpenLoop{Task: s, Priority: "", SourceID: ""}
	}
	return nil
}
