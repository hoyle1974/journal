package prompts_test

import (
	"strings"
	"testing"

	"github.com/jackstrohm/jot/internal/prompts"
)

func TestBuildRelationshipExtractor_ContainsContent(t *testing.T) {
	data := prompts.RelationshipExtractorData{
		Content: "Gloria is my wife and she loves hiking.",
	}
	out, err := prompts.BuildRelationshipExtractor(data)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(out, "Gloria is my wife") {
		t.Errorf("expected content in rendered prompt, got:\n%s", out)
	}
	if !strings.Contains(out, "Subject | Predicate | Object") {
		t.Errorf("expected SPO format instructions in prompt, got:\n%s", out)
	}
}

func TestBuildRelationshipExtractor_EmptyContent(t *testing.T) {
	// Empty content must render without error (not panic).
	data := prompts.RelationshipExtractorData{Content: ""}
	_, err := prompts.BuildRelationshipExtractor(data)
	if err != nil {
		t.Fatalf("expected no error for empty content, got: %v", err)
	}
}
