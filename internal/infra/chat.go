package infra

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/genai"
)

// ExtractTextFromResponse extracts text content from a Gemini response.
func ExtractTextFromResponse(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Text()
}

// HasFunctionCalls checks if the response contains function calls.
func HasFunctionCalls(resp *genai.GenerateContentResponse) bool {
	if resp == nil {
		return false
	}
	return len(resp.FunctionCalls()) > 0
}

// ExtractFunctionCalls extracts all function calls from a response.
func ExtractFunctionCalls(resp *genai.GenerateContentResponse) []*genai.FunctionCall {
	if resp == nil {
		return nil
	}
	return resp.FunctionCalls()
}

// EmptyResponseReason returns a short reason when the API returned no text and no function calls.
func EmptyResponseReason(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return "No response from API."
	}
	if len(resp.Candidates) == 0 {
		if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != genai.BlockedReasonUnspecified {
			return fmt.Sprintf("Prompt blocked (%s).", resp.PromptFeedback.BlockReason)
		}
		return "No candidates returned."
	}
	c := resp.Candidates[0]
	if c.Content == nil || len(c.Content.Parts) == 0 {
		return fmt.Sprintf("Empty content (finish_reason=%s).", c.FinishReason)
	}
	return fmt.Sprintf("Finish reason: %s.", c.FinishReason)
}

// ExtractThinkingAndAnswer splits a response into native thinking tokens and answer text.
// Parts with Thought==true are reasoning; all other text parts are the answer.
// Returns empty strings when the response is nil or contains only function calls.
func ExtractThinkingAndAnswer(resp *genai.GenerateContentResponse) (thinking, answer string) {
	if resp == nil {
		return "", ""
	}
	var thinkParts, answerParts []string
	for _, cand := range resp.Candidates {
		if cand == nil || cand.Content == nil {
			continue
		}
		for _, p := range cand.Content.Parts {
			if p == nil {
				continue
			}
			if p.Thought && p.Text != "" {
				thinkParts = append(thinkParts, p.Text)
			} else if !p.Thought && p.Text != "" {
				answerParts = append(answerParts, p.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(thinkParts, "\n")),
		strings.TrimSpace(strings.Join(answerParts, "\n"))
}

// ChatSession manages a multi-turn conversation with Gemini.
type ChatSession struct {
	app                   *App
	chat                  *genai.Chat
	config                *genai.GenerateContentConfig
	ctx                   context.Context
	modelName             string
	lastLLMCorrelationID  string // set after each SendMessage for flow tracing
}

// NewChatSession creates a new chat session with tools enabled. app is passed explicitly by the caller.
// Set withThinking=true to enable native Gemini 2.5 thinking (ThinkingConfig{IncludeThoughts: true}).
func NewChatSession(ctx context.Context, app *App, systemPrompt string, tools []*genai.FunctionDeclaration, withThinking bool) (*ChatSession, error) {
	if app == nil {
		return nil, fmt.Errorf("app required")
	}
	client, err := app.Gemini(ctx)
	if err != nil {
		return nil, err
	}

	modelName := app.QueryModel()
	config := &genai.GenerateContentConfig{}
	if systemPrompt != "" {
		config.SystemInstruction = &genai.Content{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{{Text: utils.SanitizePrompt(systemPrompt)}},
		}
	}
	if len(tools) > 0 {
		config.Tools = []*genai.Tool{{FunctionDeclarations: tools}}
	}
	if withThinking {
		config.ThinkingConfig = &genai.ThinkingConfig{IncludeThoughts: true}
	}

	chat, err := client.Chats.Create(ctx, modelName, config, nil)
	if err != nil {
		return nil, err
	}

	LoggerFrom(ctx).Debug("chat session created",
		"model", modelName,
		"tools_count", len(tools),
		"has_system", systemPrompt != "",
	)

	return &ChatSession{
		app:       app,
		chat:      chat,
		config:    config,
		ctx:       ctx,
		modelName: modelName,
	}, nil
}

func sanitizeParts(parts []*genai.Part) []*genai.Part {
	out := make([]*genai.Part, len(parts))
	for i, p := range parts {
		if p == nil {
			continue
		}
		part := &genai.Part{}
		if p.Text != "" {
			part.Text = utils.SanitizePrompt(p.Text)
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
		out[i] = part
	}
	return out
}

// SendMessage sends a message and returns the response.
// Before the call it collects context telemetry (token counts by category); after the call it logs a single LLM_CONTEXT_AUDIT line.
func (cs *ChatSession) SendMessage(ctx context.Context, parts ...*genai.Part) (*genai.GenerateContentResponse, error) {
	llmID := GenShortRunID()
	sanitized := sanitizeParts(parts)
	inputSizeBytes := estimatePartsSize(sanitized)

	// Log full context sent to Gemini (system + history + current turn; excludes tool definitions).
	history := cs.chat.History(true)
	contextSent := formatContextSent(cs.config.SystemInstruction, history, sanitized)
	// Info: compact line only (context_len + short preview). Full context is in LLM_RAW_REQUEST at Debug.
	// Large payloads inlined into the log message can exceed Cloud Logging limits and cause the entry to be dropped.
	LoggerFrom(ctx).Info("LLM_CONTEXT_SENT | context sent to Gemini (system + history + current)",
		slog.String("event", "LLM_CONTEXT_SENT"),
		slog.Int("context_len", len(contextSent)),
		slog.String("context_preview", utils.TruncateString(contextSent, 300)),
	)
	hasTools := len(cs.config.Tools) > 0
	LogLLMRequest(ctx, llmID, cs.modelName, contextSent, hasTools)

	// Pre-call: token breakdown for context-caching analysis (system, tools, archive, recent+current).
	audit := CollectContextAudit(ctx, cs.app, cs.modelName, cs.config, history, sanitized)

	// New SDK SendMessage takes ...Part (by value). Convert *Part to Part.
	partValues := make([]genai.Part, len(sanitized))
	for i, p := range sanitized {
		if p != nil {
			partValues[i] = *p
		}
	}
	resp, err := cs.chat.SendMessage(ctx, partValues...)
	if err != nil {
		LoggerFrom(ctx).Error("chat message failed", "error", err)
		return nil, fmt.Errorf("Gemini chat error: %w", err)
	}

	cs.lastLLMCorrelationID = llmID
	LogLLMResponse(ctx, llmID, resp)
	LogLLMMetrics(ctx, llmID, cs.modelName, resp, inputSizeBytes)
	LogContextAudit(ctx, llmID, cs.modelName, audit, resp)
	return resp, nil
}

// LastLLMCorrelationID returns the correlation ID of the most recent SendMessage call.
// Use it to tie agent-level logs (e.g. FOH iteration) to LLM_RAW_REQUEST/RESPONSE/METRICS lines.
func (cs *ChatSession) LastLLMCorrelationID() string {
	return cs.lastLLMCorrelationID
}

// estimatePartsSize returns approximate byte size of parts for metrics when usage metadata is missing.
func estimatePartsSize(parts []*genai.Part) int {
	var n int
	for _, p := range parts {
		if p == nil {
			continue
		}
		n += len(p.Text)
		if p.FunctionResponse != nil {
			for k, v := range p.FunctionResponse.Response {
				n += len(k)
				if s, ok := v.(string); ok {
					n += len(s)
				}
			}
		}
	}
	return n
}

// formatPartsToText returns a single string for logging: text parts concatenated, function calls/responses
// summarized, inline data noted. Tool definitions are excluded. Used by both chat and single-shot dispatch.
func formatPartsToText(parts []*genai.Part) string {
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
				preview := fmt.Sprintf("%v", p.FunctionResponse.Response)
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				b.WriteString(preview)
			}
			continue
		}
		if p.InlineData != nil {
			b.WriteString("[image]")
			continue
		}
		b.WriteString("[part]")
	}
	return b.String()
}

// formatContextSent builds the full context (system + history + current) sent to Gemini for logging. Excludes tool definitions.
func formatContextSent(systemInstruction *genai.Content, history []*genai.Content, currentParts []*genai.Part) string {
	var sections []string
	if systemInstruction != nil && len(systemInstruction.Parts) > 0 {
		sections = append(sections, "=== system ===", formatPartsToText(systemInstruction.Parts))
	}
	if len(history) > 0 {
		sections = append(sections, "=== history ===")
		for _, c := range history {
			if c == nil {
				continue
			}
			role := c.Role
			if role == "" {
				role = "unknown"
			}
			sections = append(sections, fmt.Sprintf("--- %s ---", role), formatPartsToText(c.Parts))
		}
	}
	if len(currentParts) > 0 {
		sections = append(sections, "=== current turn ===", formatPartsToText(currentParts))
	}
	return strings.Join(sections, "\n")
}

// TrimHistory is a no-op with the new SDK: the genai.Chat type does not expose history trimming.
// History is still managed by the SDK; consider starting a new chat if context must be reduced.
func (cs *ChatSession) TrimHistory(maxPairs int) {
	_ = maxPairs
	LoggerFrom(cs.ctx).Debug("TrimHistory called (no-op with google.golang.org/genai Chat)")
}
