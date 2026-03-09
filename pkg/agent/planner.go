package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/llmjson"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

// PlanPhase represents a single step in a generated plan.
type PlanPhase struct {
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Dependencies []string `json:"dependencies"`
}

// GeneratedPlan represents the root output from the LLM.
type GeneratedPlan struct {
	Phases []PlanPhase `json:"phases"`
}

// CreateAndSavePlan forces Gemini to decompose a goal into JSON, then saves it to the Knowledge Graph.
func CreateAndSavePlan(ctx context.Context, goal string) (string, error) {
	ctx, span := infra.StartSpan(ctx, "plan.create_and_save")
	defer span.End()

	app := infra.GetApp(ctx)
	if app == nil {
		return "", fmt.Errorf("no app in context")
	}
	schema := &genai.Schema{
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
	prompt := fmt.Sprintf("Create a detailed, sequential plan to achieve this goal:\n%s\nBreak it down into clear phases with titles, descriptions, and any dependencies between phases.", utils.WrapAsUserData(utils.SanitizePrompt(goal)))
	req := &infra.LLMRequest{
		SystemPrompt:   prompts.PlanSystem(),
		Parts:          []genai.Part{genai.Text(prompt)},
		Model:          app.Config().GeminiModel,
		GenConfig:      &infra.GenConfig{MaxOutputTokens: 2048, ResponseMIMEType: "application/json"},
		ResponseSchema: schema,
	}
	resp, err := app.Dispatch(ctx, req)
	if err != nil {
		span.RecordError(err)
		return "", fmt.Errorf("failed to generate plan: %w", err)
	}

	jsonText := infra.ExtractTextFromResponse(resp)
	plan, err := parsePlanJSON(jsonText)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	parentID, err := memory.UpsertKnowledge(ctx, goal, "goal", `{"status": "planning"}`, nil)
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
		phaseID, _ := memory.UpsertKnowledge(ctx, fmt.Sprintf("%s: %s", phase.Title, phase.Description), "task", string(metaBytes), nil)
		resultLines = append(resultLines, fmt.Sprintf("%d. %s (Task ID: %s)", i+1, phase.Title, phaseID))
	}

	span.SetAttributes(map[string]string{
		"goal_id":     parentID,
		"phase_count": fmt.Sprintf("%d", len(plan.Phases)),
	})

	infra.LoggerFrom(ctx).Info("plan created",
		"goal_id", parentID,
		"phases", len(plan.Phases),
	)

	return strings.Join(resultLines, "\n"), nil
}

// ParsePlanJSON parses JSON text into a GeneratedPlan. Exported for testing.
func ParsePlanJSON(jsonText string) (*GeneratedPlan, error) {
	return parsePlanJSON(jsonText)
}

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
