package gemini

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"github.com/jackstrohm/jot/pkg/memory"
)

const (
	defaultMaxOutputTokens = 8192
	minMaxOutputTokens     = 4096
)

type dispatcher struct {
	client *genai.Client
	model  string
}

// NewDispatcher returns a memory.LLMDispatcher backed by the Gemini SDK.
// client is the existing genai.Client from infra.App (avoids creating a second client).
// model is the resolved model name (e.g. "gemini-2.0-flash").
func NewDispatcher(client *genai.Client, model string) memory.LLMDispatcher {
	return &dispatcher{client: client, model: model}
}

func (d *dispatcher) Dispatch(ctx context.Context, req memory.LLMRequest) (string, error) {
	maxOut := req.MaxTokens
	if maxOut <= 0 {
		maxOut = defaultMaxOutputTokens
	}
	if maxOut < minMaxOutputTokens {
		maxOut = minMaxOutputTokens
	}

	cfg := &genai.GenerateContentConfig{
		MaxOutputTokens: int32(maxOut),
		SafetySettings: []*genai.SafetySetting{
			{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockThresholdBlockNone},
		},
	}
	if req.JSONMode {
		cfg.ResponseMIMEType = "application/json"
	}
	if req.SystemPrompt != "" {
		cfg.SystemInstruction = &genai.Content{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{{Text: req.SystemPrompt}},
		}
	}

	contents := []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: req.UserPrompt}}}}
	resp, err := d.client.Models.GenerateContent(ctx, d.model, contents, cfg)
	if err != nil {
		return "", fmt.Errorf("gemini dispatch: %w", err)
	}
	text := strings.TrimSpace(resp.Text())
	if text == "" {
		return "", fmt.Errorf("gemini returned empty response")
	}
	return text, nil
}
