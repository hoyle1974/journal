package jot

import (
	"testing"

	"github.com/google/generative-ai-go/genai"
)

func TestExtractTextFromResponse(t *testing.T) {
	tests := []struct {
		name     string
		resp     *genai.GenerateContentResponse
		expected string
	}{
		{
			name:     "nil response",
			resp:     nil,
			expected: "",
		},
		{
			name:     "empty candidates",
			resp:     &genai.GenerateContentResponse{Candidates: []*genai.Candidate{}},
			expected: "",
		},
		{
			name: "valid text response",
			resp: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []genai.Part{genai.Text("Hello, world!")},
						},
					},
				},
			},
			expected: "Hello, world!",
		},
		{
			name: "nil content",
			resp: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{Content: nil},
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTextFromResponse(tt.resp)
			if result != tt.expected {
				t.Errorf("extractTextFromResponse() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestHasFunctionCalls(t *testing.T) {
	tests := []struct {
		name     string
		resp     *genai.GenerateContentResponse
		expected bool
	}{
		{
			name:     "nil response",
			resp:     nil,
			expected: false,
		},
		{
			name: "text only response",
			resp: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []genai.Part{genai.Text("Hello")},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "function call response",
			resp: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []genai.Part{
								genai.FunctionCall{
									Name: "get_todos",
									Args: map[string]any{"status": "pending"},
								},
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasFunctionCalls(tt.resp)
			if result != tt.expected {
				t.Errorf("HasFunctionCalls() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExtractFunctionCalls(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []genai.Part{
						genai.FunctionCall{
							Name: "get_todos",
							Args: map[string]any{"status": "pending"},
						},
						genai.FunctionCall{
							Name: "get_entries",
							Args: map[string]any{"limit": float64(10)},
						},
					},
				},
			},
		},
	}

	calls := ExtractFunctionCalls(resp)
	if len(calls) != 2 {
		t.Errorf("ExtractFunctionCalls() returned %d calls, want 2", len(calls))
	}

	if calls[0].Name != "get_todos" {
		t.Errorf("First call name = %q, want %q", calls[0].Name, "get_todos")
	}

	if calls[1].Name != "get_entries" {
		t.Errorf("Second call name = %q, want %q", calls[1].Name, "get_entries")
	}
}
