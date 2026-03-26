package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/utils"
)

// EvaluatorExtract holds the result of running the evaluator LLM on an entry (no storage).
type EvaluatorExtract struct {
	Significance     float64
	Domain           string
	FutureCommitment float64 // 0-1: extent to which the entry expresses a commitment to do something
	CommitmentIntent string  // one sentence describing the action, if future_commitment is high
}

// RunEvaluatorExtract runs the evaluator LLM on content and returns significance/domain plus commitment signals.
func RunEvaluatorExtract(ctx context.Context, app *infra.App, content string) (*EvaluatorExtract, error) {
	if len(strings.TrimSpace(content)) < 10 {
		return nil, nil
	}
	if app == nil {
		return nil, fmt.Errorf("app required")
	}
	systemPrompt := prompts.Evaluator() + prompts.DataSafety()
	prompt := fmt.Sprintf("Entry:\n%s", utils.WrapAsUserData(utils.SanitizePrompt(content)))

	bgCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req := &infra.LLMRequest{
		SystemPrompt: systemPrompt,
		Parts:        []*genai.Part{{Text: prompt}},
		Model:        app.QueryModel(),
		GenConfig:    &infra.GenConfig{MaxOutputTokens: 1024},
	}
	resp, err := app.Dispatch(bgCtx, req)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	simple, _ := utils.ParseKeyValueMap(text)
	sig, _ := strconv.ParseFloat(strings.TrimSpace(simple["significance"]), 64)
	fc, _ := strconv.ParseFloat(strings.TrimSpace(simple["future_commitment"]), 64)
	out := &EvaluatorExtract{
		Significance:     sig,
		Domain:           strings.TrimSpace(simple["domain"]),
		FutureCommitment: fc,
		CommitmentIntent: strings.TrimSpace(simple["commitment_intent"]),
	}
	if out.Significance < 0 {
		out.Significance = 0
	}
	if out.Significance > 1 {
		out.Significance = 1
	}
	if out.Domain == "" {
		out.Domain = "thought"
	}
	if out.FutureCommitment < 0 {
		out.FutureCommitment = 0
	}
	if out.FutureCommitment > 1 {
		out.FutureCommitment = 1
	}
	return out, nil
}

// AgencyTaskCommitmentThreshold is the minimum future_commitment score for the Loom task worker
// to auto-create a task node from a log entry.
const AgencyTaskCommitmentThreshold = 0.6

// MinCommitmentIntentLen is the minimum length of commitment_intent to auto-create a task (avoid vague commitments).
const MinCommitmentIntentLen = 10

