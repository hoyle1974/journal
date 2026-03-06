package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

// GenerateToolCode uses the LLM to write a Go file implementing a new tool.
func GenerateToolCode(ctx context.Context, env PlannerEnv, request, userNotes string) (string, error) {
	ctx, span := infra.StartSpan(ctx, "agent.generate_tool_code")
	defer span.End()

	_ = env // reserved for future use (e.g. saving drafts via UpsertKnowledge)

	app := infra.GetApp(ctx)
	if app == nil {
		return "", fmt.Errorf("no app in context")
	}
	client, err := app.Gemini(ctx)
	if err != nil {
		return "", err
	}

	model := client.GenerativeModel(app.QueryModel())
	model.SetMaxOutputTokens(4096)

	systemPrompt := `You are a Principal Go Engineer extending the JOT Agentic framework.
Your task is to write the Go implementation for a new tool based on the system's request and the user's approval.
If the user's notes indicate rejection or cancellation (e.g., "no", "skip", "don't build this"), output ONLY the word REJECTED.
Otherwise, output ONLY valid Go code. Use the 'tools.Register()' pattern. Do not wrap the code in markdown blocks like 'go'.`

	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text(systemPrompt)}}

	userPrompt := fmt.Sprintf("Tool Request:\n%s\n\nUser Notes/Approval:\n%s\n\nWrite the internal/tools/impl/<tool>.go file implementation.",
		utils.WrapAsUserData(utils.SanitizePrompt(request)),
		utils.WrapAsUserData(utils.SanitizePrompt(userNotes)))

	infra.GeminiCallsTotal.Inc()
	resp, err := model.GenerateContent(ctx, genai.Text(userPrompt))
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	text := strings.TrimSpace(infra.ExtractTextFromResponse(resp))

	// Clean up possible markdown fences just in case the LLM disobeys
	text = strings.TrimPrefix(text, "```go")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")

	return strings.TrimSpace(text), nil
}
