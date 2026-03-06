package infra

import (
	"context"
	"fmt"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/pkg/utils"
)

// ExtractTextFromResponse extracts text content from a Gemini response.
func ExtractTextFromResponse(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return ""
	}
	candidate := resp.Candidates[0]
	if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
		return ""
	}
	for _, part := range candidate.Content.Parts {
		if text, ok := part.(genai.Text); ok {
			return string(text)
		}
	}
	return ""
}

// HasFunctionCalls checks if the response contains function calls.
func HasFunctionCalls(resp *genai.GenerateContentResponse) bool {
	if resp == nil || len(resp.Candidates) == 0 {
		return false
	}
	candidate := resp.Candidates[0]
	if candidate.Content == nil {
		return false
	}
	for _, part := range candidate.Content.Parts {
		if _, ok := part.(genai.FunctionCall); ok {
			return true
		}
	}
	return false
}

// ExtractFunctionCalls extracts all function calls from a response.
func ExtractFunctionCalls(resp *genai.GenerateContentResponse) []genai.FunctionCall {
	var calls []genai.FunctionCall
	if resp == nil || len(resp.Candidates) == 0 {
		return calls
	}
	candidate := resp.Candidates[0]
	if candidate.Content == nil {
		return calls
	}
	for _, part := range candidate.Content.Parts {
		if fc, ok := part.(genai.FunctionCall); ok {
			calls = append(calls, fc)
		}
	}
	return calls
}

// EmptyResponseReason returns a short reason when the API returned no text and no function calls.
func EmptyResponseReason(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return "No response from API."
	}
	if len(resp.Candidates) == 0 {
		if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != genai.BlockReasonUnspecified {
			return fmt.Sprintf("Prompt blocked (%s).", resp.PromptFeedback.BlockReason.String())
		}
		return "No candidates returned."
	}
	c := resp.Candidates[0]
	if c.Content == nil || len(c.Content.Parts) == 0 {
		return fmt.Sprintf("Empty content (finish_reason=%s).", c.FinishReason.String())
	}
	return fmt.Sprintf("Finish reason: %s.", c.FinishReason.String())
}

// ChatSession manages a multi-turn conversation with Gemini.
type ChatSession struct {
	model     *genai.GenerativeModel
	session   *genai.ChatSession
	ctx       context.Context
	modelName string
}

// NewChatSession creates a new chat session with tools enabled.
func NewChatSession(ctx context.Context, systemPrompt string, tools []*genai.FunctionDeclaration) (*ChatSession, error) {
	ctx, span := StartSpan(ctx, "gemini.new_chat_session")
	defer span.End()

	app := GetApp(ctx)
	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}
	client, err := app.Gemini(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	modelName := app.QueryModel()
	model := client.GenerativeModel(modelName)

	if systemPrompt != "" {
		model.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(utils.SanitizePrompt(systemPrompt))},
		}
	}

	if len(tools) > 0 {
		model.Tools = []*genai.Tool{{
			FunctionDeclarations: tools,
		}}
	}

	session := model.StartChat()

	LoggerFrom(ctx).Debug("chat session created",
		"model", modelName,
		"tools_count", len(tools),
		"has_system", systemPrompt != "",
	)

	return &ChatSession{
		model:     model,
		session:   session,
		ctx:       ctx,
		modelName: modelName,
	}, nil
}

func sanitizeParts(parts []genai.Part) []genai.Part {
	out := make([]genai.Part, len(parts))
	for i, part := range parts {
		switch p := part.(type) {
		case genai.Text:
			out[i] = genai.Text(utils.SanitizePrompt(string(p)))
		case genai.FunctionResponse:
			sanitizedResp := make(map[string]any)
			for k, v := range p.Response {
				if s, ok := v.(string); ok {
					sanitizedResp[k] = utils.SanitizePrompt(s)
				} else {
					sanitizedResp[k] = v
				}
			}
			out[i] = genai.FunctionResponse{Name: p.Name, Response: sanitizedResp}
		default:
			out[i] = part
		}
	}
	return out
}

// SendMessage sends a message and returns the response.
func (cs *ChatSession) SendMessage(ctx context.Context, parts ...genai.Part) (*genai.GenerateContentResponse, error) {
	ctx, span := StartSpan(ctx, "gemini.send_message")
	defer span.End()

	sanitized := sanitizeParts(parts)
	inputSizeBytes := estimatePartsSize(parts)
	resp, err := cs.session.SendMessage(ctx, sanitized...)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("chat message failed", "error", err)
		return nil, fmt.Errorf("Gemini chat error: %w", err)
	}
	LogLLMMetrics(ctx, cs.modelName, resp, inputSizeBytes)
	return resp, nil
}

// estimatePartsSize returns approximate byte size of parts for metrics when usage metadata is missing.
func estimatePartsSize(parts []genai.Part) int {
	var n int
	for _, p := range parts {
		switch part := p.(type) {
		case genai.Text:
			n += len(part)
		case genai.FunctionResponse:
			for k, v := range part.Response {
				n += len(k)
				if s, ok := v.(string); ok {
					n += len(s)
				}
			}
		default:
			// ignore
		}
	}
	return n
}

// AddFunctionResponse adds a function response to the conversation history.
func (cs *ChatSession) AddFunctionResponse(name string, response map[string]any) genai.Part {
	return genai.FunctionResponse{
		Name:     name,
		Response: response,
	}
}

// GetHistory returns the current conversation history.
func (cs *ChatSession) GetHistory() []*genai.Content {
	return cs.session.History
}

// TrimHistory keeps only the last n message pairs in history.
func (cs *ChatSession) TrimHistory(maxPairs int) {
	history := cs.session.History
	maxMessages := maxPairs * 2
	if len(history) > maxMessages {
		cs.session.History = history[len(history)-maxMessages:]
		LoggerFrom(cs.ctx).Debug("chat history trimmed",
			"from", len(history),
			"to", len(cs.session.History),
		)
	}
}
