package gemini

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hoyle1974/memory"
)

const (
	defaultMaxOutputTokens = 8192
	minMaxOutputTokens     = 4096
)

type dispatcher struct {
	client *genai.Client
	model  string
}

// isSafetyResponse returns true if the Gemini API blocked generation for safety reasons.
// It matches only FinishReasonSafety. Other safety-adjacent reasons (BLOCKLIST,
// PROHIBITED_CONTENT, SPII, IMAGE_SAFETY, IMAGE_PROHIBITED_CONTENT) are not matched
// and will produce an empty-response error instead.
func isSafetyResponse(resp *genai.GenerateContentResponse) bool {
	if resp == nil {
		return false
	}
	for _, c := range resp.Candidates {
		if c.FinishReason == genai.FinishReasonSafety {
			return true
		}
	}
	return false
}

// classifyAPIError maps known Gemini/gRPC transient errors to typed MemoryErrors.
// Unrecognised errors are wrapped with the "gemini dispatch" prefix and passed through.
func classifyAPIError(err error) error {
	code := status.Code(err)
	if errors.Is(err, context.DeadlineExceeded) || code == codes.DeadlineExceeded {
		return &memory.MemoryError{Code: memory.CodeLLMTimeout, Message: "gemini API timeout", Err: err}
	}
	if code == codes.ResourceExhausted {
		return &memory.MemoryError{Code: memory.CodeLLMRateLimit, Message: "gemini rate limit exceeded", Err: err}
	}
	return fmt.Errorf("gemini dispatch: %w", err)
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
		return "", classifyAPIError(err)
	}
	if isSafetyResponse(resp) {
		return "", fmt.Errorf("gemini dispatch: %w", memory.ErrLLMSafetyTrip)
	}
	text := strings.TrimSpace(resp.Text())
	if text == "" {
		return "", fmt.Errorf("gemini returned empty response")
	}
	return text, nil
}
