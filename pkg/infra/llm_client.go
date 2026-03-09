package infra

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/pkg/utils"
)

// LLMRequest is the unified request structure for the dispatcher.
// Used for single-shot calls; multi-turn uses ChatSession which shares the same logging helpers.
type LLMRequest struct {
	SystemPrompt    string
	Parts           []genai.Part
	Tools           []*genai.FunctionDeclaration
	Model           string // empty = use app default
	GenConfig       *GenConfig
	ResponseSchema  *genai.Schema // optional JSON schema for response
}

// LogLLMRequest logs the full request context at DEBUG (uncensored).
// requestContext is the full text sent to the model (system + messages; no tool definitions).
func LogLLMRequest(ctx context.Context, model string, requestContext string, hasTools bool) {
	LoggerFrom(ctx).Debug("LLM_RAW_REQUEST",
		slog.String("event", "LLM_RAW_REQUEST"),
		slog.String("model", model),
		slog.String("request_context", requestContext),
		slog.Bool("has_tools", hasTools),
	)
}

// LogLLMResponse logs the full response at DEBUG (uncensored).
func LogLLMResponse(ctx context.Context, resp *genai.GenerateContentResponse) {
	if resp == nil {
		LoggerFrom(ctx).Debug("LLM_RAW_RESPONSE", slog.String("event", "LLM_RAW_RESPONSE"), slog.String("text", ""), slog.String("finish_reason", "no_response"))
		return
	}
	text := ExtractTextFromResponse(resp)
	finishReason := ""
	if len(resp.Candidates) > 0 && resp.Candidates[0] != nil {
		finishReason = resp.Candidates[0].FinishReason.String()
	}
	LoggerFrom(ctx).Debug("LLM_RAW_RESPONSE",
		slog.String("event", "LLM_RAW_RESPONSE"),
		slog.String("text", text),
		slog.String("finish_reason", finishReason),
	)
}

func formatPartsForLog(parts []genai.Part) string {
	var b strings.Builder
	for _, p := range parts {
		switch part := p.(type) {
		case genai.Text:
			b.WriteString(string(part))
		case genai.FunctionCall:
			b.WriteString(fmt.Sprintf("[function_call: %s]", part.Name))
		case genai.FunctionResponse:
			b.WriteString(fmt.Sprintf("[tool_result: %s]", part.Name))
			if len(part.Response) > 0 {
				b.WriteString(" ")
				b.WriteString(fmt.Sprintf("%v", part.Response))
			}
		default:
			b.WriteString("[part]")
		}
	}
	return b.String()
}

// Dispatch runs a single-shot LLM call through the central choke point: logs request (DEBUG), calls Gemini, logs response (DEBUG).
// Also runs LogLLMMetrics and, when app and session history are available, CollectContextAudit/LogContextAudit (for single-shot we have no history, so audit is minimal).
// Caller must have app in context; returns error if app is nil or Gemini fails.
func (a *App) Dispatch(ctx context.Context, req *LLMRequest) (*genai.GenerateContentResponse, error) {
	if a == nil || a.Config() == nil {
		return nil, fmt.Errorf("no app or config for LLM dispatch")
	}
	ctx, span := StartSpan(ctx, "gemini.dispatch")
	defer span.End()

	client, err := a.Gemini(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	modelName := a.EffectiveModel(a.Config().GeminiModel)
	if req.Model != "" {
		modelName = a.EffectiveModel(req.Model)
	}
	if req.GenConfig != nil && req.GenConfig.ModelOverride != "" {
		modelName = a.EffectiveModel(req.GenConfig.ModelOverride)
	}
	model := client.GenerativeModel(modelName)

	if req.SystemPrompt != "" {
		model.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(utils.SanitizePrompt(req.SystemPrompt))},
		}
	}
	if len(req.Tools) > 0 {
		model.Tools = []*genai.Tool{{FunctionDeclarations: req.Tools}}
	}
	if req.GenConfig != nil {
		if req.GenConfig.Temperature > 0 {
			model.SetTemperature(float32(req.GenConfig.Temperature))
		}
		if req.GenConfig.TopP > 0 {
			model.SetTopP(float32(req.GenConfig.TopP))
		}
		if req.GenConfig.MaxOutputTokens > 0 {
			model.SetMaxOutputTokens(int32(req.GenConfig.MaxOutputTokens))
		}
		if req.GenConfig.ResponseMIMEType != "" {
			model.ResponseMIMEType = req.GenConfig.ResponseMIMEType
		}
	}
	if req.ResponseSchema != nil {
		model.ResponseSchema = req.ResponseSchema
	}

	requestContext := req.SystemPrompt
	if len(requestContext) > 0 {
		requestContext += "\n\n"
	}
	requestContext += formatPartsForLog(req.Parts)
	hasTools := len(req.Tools) > 0
	LogLLMRequest(ctx, modelName, requestContext, hasTools)

	sanitized := make([]genai.Part, len(req.Parts))
	for i, p := range req.Parts {
		if t, ok := p.(genai.Text); ok {
			sanitized[i] = genai.Text(utils.SanitizePrompt(string(t)))
		} else {
			sanitized[i] = p
		}
	}
	inputSizeBytes := 0
	for _, p := range sanitized {
		if t, ok := p.(genai.Text); ok {
			inputSizeBytes += len(t)
		}
	}
	inputSizeBytes += len(req.SystemPrompt)

	resp, err := model.GenerateContent(ctx, sanitized...)
	if err != nil {
		span.RecordError(err)
		if resp != nil {
			LogLLMResponse(ctx, resp)
		}
		return nil, err
	}
	LogLLMResponse(ctx, resp)
	LogLLMMetrics(ctx, modelName, resp, inputSizeBytes)
	return resp, nil
}
