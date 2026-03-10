package infra

import (
	"testing"

	"google.golang.org/genai"
)

func TestExtractText(t *testing.T) {
	tests := []struct {
		name     string
		resp     *genai.GenerateContentResponse
		expected string
	}{
		{"nil response", nil, ""},
		{"empty candidates", &genai.GenerateContentResponse{Candidates: []*genai.Candidate{}}, ""},
		{
			"valid text response",
			&genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{Parts: []*genai.Part{{Text: "Hello, world!"}}},
				}},
			},
			"Hello, world!",
		},
		{
			"nil content",
			&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{Content: nil}}},
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractText(tt.resp)
			if got != tt.expected {
				t.Errorf("ExtractText() = %q, want %q", got, tt.expected)
			}
		})
	}
}
