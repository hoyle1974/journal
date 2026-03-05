package jot

import (
	"context"

	"github.com/jackstrohm/jot/pkg/agent"
)

// PlanPhase and GeneratedPlan are re-exported from agent for compatibility.
type PlanPhase = agent.PlanPhase
type GeneratedPlan = agent.GeneratedPlan

// CreateAndSavePlan forces Gemini to decompose a goal into JSON, then saves it to the Knowledge Graph.
func CreateAndSavePlan(ctx context.Context, goal string) (string, error) {
	return agent.CreateAndSavePlan(ctx, jotFOHEnv{}, goal)
}

// ParsePlanJSON is re-exported for tests.
func ParsePlanJSON(jsonText string) (*GeneratedPlan, error) {
	return agent.ParsePlanJSON(jsonText)
}
