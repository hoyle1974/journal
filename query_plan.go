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

// PlanPhase represents a single step in a generated plan
type PlanPhase struct {
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Dependencies []string `json:"dependencies"`
}

// GeneratedPlan represents the root output from the LLM
type GeneratedPlan struct {
	Phases []PlanPhase `json:"phases"`
}

// CreateAndSavePlan forces Gemini to decompose a goal into JSON, then saves it to the Knowledge Graph.
func CreateAndSavePlan(ctx context.Context, goal string) (string, error) {
	ctx, span := StartSpan(ctx, "plan.create_and_save")
	defer span.End()

	client, err := GetGeminiClient(ctx)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	model := client.GenerativeModel(GetEffectiveModel(ctx, GeminiModel))
	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text(prompts.PlanSystem())}}
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(2048)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"phases": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"title":        {Type: genai.TypeString},
						"description":  {Type: genai.TypeString},
						"dependencies": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
					},
				},
			},
		},
	}

	prompt := fmt.Sprintf("Create a detailed, sequential plan to achieve this goal:\n%s\nBreak it down into clear phases with titles, descriptions, and any dependencies between phases.", WrapAsUserData(SanitizePrompt(goal)))
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		span.RecordError(err)
		return "", fmt.Errorf("failed to generate plan: %w", err)
	}

	jsonText := extractTextFromResponse(resp)
	plan, err := parsePlanJSON(jsonText)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	parentID, err := UpsertKnowledge(ctx, goal, "goal", `{"status": "planning"}`, nil)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	var resultLines []string
	resultLines = append(resultLines, fmt.Sprintf("Created plan for: %s (ID: %s)", goal, parentID))

	for i, phase := range plan.Phases {
		metadataMeta := map[string]interface{}{
			"parent_goal":  parentID,
			"step_number":  i + 1,
			"dependencies": phase.Dependencies,
			"status":       "pending",
		}
		metaBytes, _ := json.Marshal(metadataMeta)

		phaseID, _ := UpsertKnowledge(ctx, fmt.Sprintf("%s: %s", phase.Title, phase.Description), "task", string(metaBytes), nil)

		resultLines = append(resultLines, fmt.Sprintf("%d. %s (Task ID: %s)", i+1, phase.Title, phaseID))
	}

	span.SetAttributes(map[string]string{
		"goal_id":     parentID,
		"phase_count": fmt.Sprintf("%d", len(plan.Phases)),
	})

	LoggerFrom(ctx).Info("plan created",
		"goal_id", parentID,
		"phases", len(plan.Phases),
	)

	return strings.Join(resultLines, "\n"), nil
}

// parsePlanJSON parses JSON text into a GeneratedPlan. Exported for testing.
func parsePlanJSON(jsonText string) (*GeneratedPlan, error) {
	plan, parseErr := llmjson.ParseLLMResponse[GeneratedPlan](jsonText, []string{"phases"})
	if plan == nil {
		if parseErr == nil {
			parseErr = fmt.Errorf("parse failed")
		}
		return nil, fmt.Errorf("failed to parse plan JSON: %w", parseErr)
	}
	return plan, nil
}
