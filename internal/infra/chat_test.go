package infra

import (
	"testing"

	"google.golang.org/genai"
)

func TestExtractThinkingAndAnswer_ThoughtPart(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{Text: "I should search for X", Thought: true},
						{Text: "Here is the answer."},
					},
				},
			},
		},
	}
	thinking, answer := ExtractThinkingAndAnswer(resp)
	if thinking != "I should search for X" {
		t.Errorf("thinking = %q, want %q", thinking, "I should search for X")
	}
	if answer != "Here is the answer." {
		t.Errorf("answer = %q, want %q", answer, "Here is the answer.")
	}
}

func TestExtractThinkingAndAnswer_NilResp(t *testing.T) {
	thinking, answer := ExtractThinkingAndAnswer(nil)
	if thinking != "" || answer != "" {
		t.Errorf("expected empty strings for nil resp")
	}
}

func TestExtractThinkingAndAnswer_OnlyFunctionCalls(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{FunctionCall: &genai.FunctionCall{Name: "semantic_search"}},
					},
				},
			},
		},
	}
	thinking, answer := ExtractThinkingAndAnswer(resp)
	if thinking != "" || answer != "" {
		t.Errorf("expected empty strings when only function calls present")
	}
}

