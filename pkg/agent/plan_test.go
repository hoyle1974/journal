package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestParsePlanJSON(t *testing.T) {
	tests := []struct {
		name       string
		jsonText   string
		wantErr    bool
		wantPhases int
	}{
		{
			name: "valid plan with phases",
			jsonText: `{
				"phases": [
					{"title": "Phase 1", "description": "First step", "dependencies": []},
					{"title": "Phase 2", "description": "Second step", "dependencies": ["Phase 1"]}
				]
			}`,
			wantErr:    false,
			wantPhases: 2,
		},
		{
			name:       "valid plan with empty phases",
			jsonText:   `{"phases": []}`,
			wantErr:    false,
			wantPhases: 0,
		},
		{
			name: "valid plan with dependencies",
			jsonText: `{
				"phases": [
					{"title": "Setup", "description": "Initial setup", "dependencies": []},
					{"title": "Build", "description": "Build phase", "dependencies": ["Setup"]},
					{"title": "Deploy", "description": "Deployment", "dependencies": ["Build"]}
				]
			}`,
			wantErr:    false,
			wantPhases: 3,
		},
		{
			name:     "invalid JSON",
			jsonText: `{invalid json}`,
			wantErr:  true,
		},
		{
			name:     "empty string",
			jsonText: "",
			wantErr:  true,
		},
		{
			name:       "null phases",
			jsonText:   `{"phases": null}`,
			wantErr:    false,
			wantPhases: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := ParsePlanJSON(tt.jsonText)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParsePlanJSON() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("ParsePlanJSON() unexpected error: %v", err)
				return
			}
			if plan == nil {
				t.Errorf("ParsePlanJSON() returned nil plan")
				return
			}
			phaseCount := len(plan.Phases)
			if phaseCount != tt.wantPhases {
				t.Errorf("ParsePlanJSON() phases count = %d, want %d", phaseCount, tt.wantPhases)
			}
		})
	}
}

func TestParsePlanJSON_PhaseStructure(t *testing.T) {
	jsonText := `{
		"phases": [
			{"title": "Research", "description": "Gather requirements", "dependencies": []},
			{"title": "Design", "description": "Create architecture", "dependencies": ["Research"]}
		]
	}`

	plan, err := ParsePlanJSON(jsonText)
	if err != nil {
		t.Fatalf("ParsePlanJSON() error: %v", err)
	}

	if len(plan.Phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(plan.Phases))
	}

	if plan.Phases[0].Title != "Research" {
		t.Errorf("phase 0 title = %q, want %q", plan.Phases[0].Title, "Research")
	}
	if plan.Phases[0].Description != "Gather requirements" {
		t.Errorf("phase 0 description = %q, want %q", plan.Phases[0].Description, "Gather requirements")
	}
	if len(plan.Phases[0].Dependencies) != 0 {
		t.Errorf("phase 0 dependencies = %v, want []", plan.Phases[0].Dependencies)
	}

	if plan.Phases[1].Title != "Design" {
		t.Errorf("phase 1 title = %q, want %q", plan.Phases[1].Title, "Design")
	}
	if len(plan.Phases[1].Dependencies) != 1 || plan.Phases[1].Dependencies[0] != "Research" {
		t.Errorf("phase 1 dependencies = %v, want [Research]", plan.Phases[1].Dependencies)
	}
}

func TestGeneratedPlan_FormatOutput(t *testing.T) {
	plan := &GeneratedPlan{
		Phases: []PlanPhase{
			{Title: "Step 1", Description: "Do first thing", Dependencies: []string{}},
			{Title: "Step 2", Description: "Do second thing", Dependencies: []string{"Step 1"}},
		},
	}

	goal := "Test goal"
	parentID := "fake-uuid-123"
	var resultLines []string
	resultLines = append(resultLines, "Created plan for: "+goal+" (ID: "+parentID+")")
	for i, phase := range plan.Phases {
		phaseID := "phase-" + phase.Title
		resultLines = append(resultLines, fmt.Sprintf("%d. %s (Task ID: %s)", i+1, phase.Title, phaseID))
	}
	output := strings.Join(resultLines, "\n")

	if !strings.Contains(output, "Created plan for: Test goal") {
		t.Errorf("output missing goal line: %s", output)
	}
	if !strings.Contains(output, "1. Step 1 (Task ID: phase-Step 1)") {
		t.Errorf("output missing phase 1: %s", output)
	}
	if !strings.Contains(output, "2. Step 2 (Task ID: phase-Step 2)") {
		t.Errorf("output missing phase 2: %s", output)
	}
}
