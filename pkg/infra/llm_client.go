package infra

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/genai"
)

// DefaultMaxOutputTokens is used when GenConfig.MaxOutputTokens is 0 or unset,
// so we never leave the model with an API/SDK default that can truncate at ~5-9 tokens.
const DefaultMaxOutputTokens = 8192

// MinMaxOutputTokens is a floor so we never send a value the API might truncate with (e.g. 1–10).
// Note: Logs confirm we send 256/512 here, but Gemini API can still return FinishReasonMaxTokens at ~5–9
// completion tokens (known API/SDK behavior). See e.g. googleapis/python-genai#782, Gemini troubleshooting.
const MinMaxOutputTokens = 4096

// LLMRequest is the unified request structure for the dispatcher.
// Used for single-shot calls; multi-turn uses ChatSession which shares the same logging helpers.
type LLMRequest struct {
	SystemPrompt   string
	Parts          []*genai.Part
	Tools          []*genai.FunctionDeclaration
	Model          string // empty = use app default
	GenConfig      *GenConfig
	ResponseSchema *genai.Schema // optional JSON schema for response
}

// genLLMCorrelationID returns a short hex ID to correlate one LLM request with its response and metrics.
func genLLMCorrelationID() string {
	return GenShortRunID()
}

// GenShortRunID returns an 8-character hex ID for correlating logs across a single run (e.g. one query or one dreamer pass).
// Use with query_run_id, dreamer_run_id, etc. so all log lines for that run can be grepped together.
func GenShortRunID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "xxxx"
	}
	return hex.EncodeToString(b)
}

// LogLLMRequest logs the full request context at DEBUG (uncensored).
// requestContext is the full text sent to the model (system + messages; no tool definitions).
// llmCorrelationID links this request to the corresponding LLM_RAW_RESPONSE, LLM_METRICS, and LLM_CONTEXT_AUDIT lines.
func LogLLMRequest(ctx context.Context, llmCorrelationID string, model string, requestContext string, hasTools bool) {
	attrs := []any{
		slog.String("event", "LLM_RAW_REQUEST"),
		slog.String("model", model),
		slog.String("request_context", requestContext),
		slog.Bool("has_tools", hasTools),
	}
	if llmCorrelationID != "" {
		attrs = append(attrs, slog.String("llm_correlation_id", llmCorrelationID))
	}
	LoggerFrom(ctx).Debug("LLM_RAW_REQUEST", attrs...)
}

// LogLLMResponse logs the full response at DEBUG (uncensored).
// llmCorrelationID should match the ID from the preceding LogLLMRequest for this call.
func LogLLMResponse(ctx context.Context, llmCorrelationID string, resp *genai.GenerateContentResponse) {
	if resp == nil {
		attrs := []any{slog.String("event", "LLM_RAW_RESPONSE"), slog.String("text", ""), slog.String("finish_reason", "no_response")}
		if llmCorrelationID != "" {
			attrs = append(attrs, slog.String("llm_correlation_id", llmCorrelationID))
		}
		LoggerFrom(ctx).Debug("LLM_RAW_RESPONSE", attrs...)
		return
	}
	text := ExtractTextFromResponse(resp)
	finishReason := ""
	if len(resp.Candidates) > 0 && resp.Candidates[0] != nil {
		finishReason = string(resp.Candidates[0].FinishReason)
	}
	attrs := []any{
		slog.String("event", "LLM_RAW_RESPONSE"),
		slog.String("text", text),
		slog.String("finish_reason", finishReason),
	}
	if llmCorrelationID != "" {
		attrs = append(attrs, slog.String("llm_correlation_id", llmCorrelationID))
	}
	// When the model returned tool calls, resp.Text() is empty and the SDK may log a warning; this is expected.
	if text == "" && HasFunctionCalls(resp) {
		attrs = append(attrs, slog.String("response_type", "tool_calls_only"))
	}
	LoggerFrom(ctx).Debug("LLM_RAW_RESPONSE", attrs...)
}

func formatPartsForLog(parts []*genai.Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p == nil {
			continue
		}
		if p.Text != "" {
			b.WriteString(p.Text)
			continue
		}
		if p.FunctionCall != nil {
			b.WriteString(fmt.Sprintf("[function_call: %s]", p.FunctionCall.Name))
			continue
		}
		if p.FunctionResponse != nil {
			b.WriteString(fmt.Sprintf("[tool_result: %s]", p.FunctionResponse.Name))
			if len(p.FunctionResponse.Response) > 0 {
				b.WriteString(" ")
				b.WriteString(fmt.Sprintf("%v", p.FunctionResponse.Response))
			}
			continue
		}
		b.WriteString("[part]")
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

	config := &genai.GenerateContentConfig{
		SafetySettings: []*genai.SafetySetting{
			{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockThresholdBlockNone},
		},
	}
	if req.SystemPrompt != "" {
		config.SystemInstruction = &genai.Content{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{{Text: utils.SanitizePrompt(req.SystemPrompt)}},
		}
	}
	if len(req.Tools) > 0 {
		config.Tools = []*genai.Tool{{FunctionDeclarations: req.Tools}}
	}

	maxOut := DefaultMaxOutputTokens
	if req.GenConfig != nil {
		if req.GenConfig.Temperature > 0 {
			t := float32(req.GenConfig.Temperature)
			config.Temperature = &t
		}
		if req.GenConfig.TopP > 0 {
			p := float32(req.GenConfig.TopP)
			config.TopP = &p
		}
		maxOut = req.GenConfig.MaxOutputTokens
		if maxOut <= 0 {
			maxOut = DefaultMaxOutputTokens
		}
		if maxOut < MinMaxOutputTokens {
			maxOut = MinMaxOutputTokens
		}
		config.MaxOutputTokens = int32(maxOut)
		LoggerFrom(ctx).Debug("dispatch gen_config", "max_output_tokens", maxOut, "model", modelName)
		if req.GenConfig.ResponseMIMEType != "" {
			config.ResponseMIMEType = req.GenConfig.ResponseMIMEType
		}
	} else {
		LoggerFrom(ctx).Debug("dispatch gen_config", "max_output_tokens", DefaultMaxOutputTokens, "model", modelName, "reason", "no_gen_config")
	}
	if req.ResponseSchema != nil {
		config.ResponseSchema = req.ResponseSchema
	}

	requestContext := req.SystemPrompt
	if len(requestContext) > 0 {
		requestContext += "\n\n"
	}
	requestContext += formatPartsForLog(req.Parts)
	hasTools := len(req.Tools) > 0
	llmID := genLLMCorrelationID()
	LogLLMRequest(ctx, llmID, modelName, requestContext, hasTools)

	sanitized := make([]*genai.Part, len(req.Parts))
	inputSizeBytes := len(req.SystemPrompt)
	for i, p := range req.Parts {
		if p == nil {
			continue
		}
		part := &genai.Part{}
		if p.Text != "" {
			part.Text = utils.SanitizePrompt(p.Text)
			inputSizeBytes += len(part.Text)
		}
		if p.FunctionCall != nil {
			part.FunctionCall = p.FunctionCall
		}
		if p.FunctionResponse != nil {
			sanitizedResp := make(map[string]any)
			for k, v := range p.FunctionResponse.Response {
				if s, ok := v.(string); ok {
					sanitizedResp[k] = utils.SanitizePrompt(s)
				} else {
					sanitizedResp[k] = v
				}
			}
			part.FunctionResponse = &genai.FunctionResponse{Name: p.FunctionResponse.Name, Response: sanitizedResp}
		}
		sanitized[i] = part
	}

	contents := []*genai.Content{{Role: genai.RoleUser, Parts: sanitized}}
	resp, err := client.Models.GenerateContent(ctx, modelName, contents, config)
	if err != nil {
		span.RecordError(err)
		if resp != nil {
			LogLLMResponse(ctx, llmID, resp)
		}
		return nil, err
	}
	LogLLMResponse(ctx, llmID, resp)
	LogLLMMetrics(ctx, llmID, modelName, resp, inputSizeBytes)
	return resp, nil
}
