package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/internal/infra"
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

// CreateAndSavePlan forces Gemini to decompose a goal into key/value output, then saves it to the Knowledge Graph.
func CreateAndSavePlan(ctx context.Context, env infra.ToolEnv, goal string) (string, error) {
	ctx, span := infra.StartSpan(ctx, "plan.create_and_save")
	defer span.End()

	if env == nil || env.Config() == nil {
		return "", fmt.Errorf("no app in context")
	}
	prompt := fmt.Sprintf("Create a detailed, sequential plan to achieve this goal:\n%s\nBreak it down into clear phases with titles, descriptions, and any dependencies between phases.", utils.WrapAsUserData(utils.SanitizePrompt(goal)))
	req := &infra.LLMRequest{
		SystemPrompt: prompts.PlanSystem(),
		Parts:        []*genai.Part{{Text: prompt}},
		Model:        env.Config().GeminiModel,
		GenConfig:    &infra.GenConfig{MaxOutputTokens: 2048},
	}
	resp, err := env.Dispatch(ctx, req)
	if err != nil {
		span.RecordError(err)
		return "", fmt.Errorf("failed to generate plan: %w", err)
	}

	text := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	infra.LoggerFrom(ctx).Debug("planner: parsing plan (K/V)", "raw_text", text)
	plan, err := parsePlanKeyValue(text)
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

// ParsePlanKeyValue parses key/value text into a GeneratedPlan. Exported for testing.
func ParsePlanKeyValue(text string) (*GeneratedPlan, error) {
	return parsePlanKeyValue(text)
}

func parsePlanKeyValue(text string) (*GeneratedPlan, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty input")
	}
	_, sections := utils.ParseKeyValueMap(text)
	phaseLines := sections["phases"]
	var phases []PlanPhase
	for _, line := range phaseLines {
		parts := strings.SplitN(line, " | ", 3)
		if len(parts) < 2 {
			continue
		}
		title := strings.TrimSpace(parts[0])
		desc := strings.TrimSpace(parts[1])
		var deps []string
		if len(parts) >= 3 && strings.TrimSpace(parts[2]) != "" {
			for _, d := range strings.Split(parts[2], ",") {
				if d := strings.TrimSpace(d); d != "" {
					deps = append(deps, d)
				}
			}
		}
		phases = append(phases, PlanPhase{Title: title, Description: desc, Dependencies: deps})
	}
	return &GeneratedPlan{Phases: phases}, nil
}
