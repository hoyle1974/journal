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

// ProactiveAlertSignificanceThreshold is the minimum significance for an entry to be
// considered for a proactive alert (e.g. selfmodel thought). Logged so you can see
// how close low-scoring entries came (e.g. "I feel dizzy" at 0.2 — tune evaluator for health).
const ProactiveAlertSignificanceThreshold = 0.8

// EvaluatorExtract holds the result of running the evaluator LLM on an entry (no storage).
type EvaluatorExtract struct {
	Significance     float64
	Domain           string
	FactToStore      string
	FutureCommitment float64 // 0-1: extent to which the entry expresses a commitment to do something
	CommitmentIntent string  // one sentence describing the action, if future_commitment is high
}

// RunEvaluatorExtract runs the evaluator LLM on content and returns significance, domain, and fact_to_store.
func RunEvaluatorExtract(ctx context.Context, app *infra.App, content string) (*EvaluatorExtract, error) {
	if len(strings.TrimSpace(content)) < 10 {
		return nil, nil
	}
	if app == nil {
		return nil, fmt.Errorf("app required")
	}
	systemPrompt := prompts.Evaluator() + prompts.DataSafety()
	prompt := ""
	node, _, err := app.Memory.FindContextByName(ctx, "user_profile")
	if err == nil && node != nil && node.Content != "" {
		profile := node.Content
		prompt = fmt.Sprintf("Relevant user preferences/facts (use when assigning domain and significance):\n%s\n\n",
			utils.TruncateString(profile, 500))
	}
	prompt += fmt.Sprintf("Entry:\n%s", utils.WrapAsUserData(utils.SanitizePrompt(content)))

	bgCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req := &infra.LLMRequest{
		SystemPrompt: systemPrompt,
		Parts:        []*genai.Part{{Text: prompt}},
		Model:        app.QueryModel(),
		GenConfig:    &infra.GenConfig{MaxOutputTokens: 1024},
	}
	infra.GeminiCallsTotal.Inc()
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
		FactToStore:      strings.TrimSpace(simple["fact_to_store"]),
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

// AgencyTaskCommitmentThreshold is the minimum future_commitment score to auto-create a task from an entry.
const AgencyTaskCommitmentThreshold = 0.6

// MinCommitmentIntentLen is the minimum length of commitment_intent to auto-create a task (avoid vague commitments).
const MinCommitmentIntentLen = 10

// RunEvaluator assigns significance to a new entry, optionally upserts high-value facts, and returns the extract for agency (task creation).
func RunEvaluator(ctx context.Context, app *infra.App, content, entryUUID, timestamp string) (*EvaluatorExtract, error) {
	ctx, span := infra.StartSpan(ctx, "evaluator.run")
	defer span.End()

	parsed, err := RunEvaluatorExtract(ctx, app, content)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("evaluator skipped", "entry_uuid", entryUUID, "reason", "extract failed", "error", err)
		return nil, err
	}
	if parsed == nil {
		infra.LoggerFrom(ctx).Info("evaluator skipped", "entry_uuid", entryUUID, "reason", "content too short or unparseable")
		return nil, nil
	}

	factStored := false
	if parsed.FactToStore != "" && parsed.Significance >= 0.5 {
		nodeType := "fact"
		if parsed.Domain == "relationship" {
			nodeType = "person"
		} else if parsed.Domain == "work" {
			nodeType = "project"
		}
		bgCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := app.Memory.UpsertSemanticMemory(bgCtx, parsed.FactToStore, nodeType, parsed.Domain, parsed.Significance, nil, []string{entryUUID}); err != nil {
			infra.LoggerFrom(ctx).Warn("evaluator upsert failed", "error", err)
		} else {
			factStored = true
			go runSPOEnrichment(context.Background(), app, parsed.FactToStore, nodeType, parsed.Domain, parsed.Significance, entryUUID)
		}
	}
	status := "IGNORE_PROACTIVE"
	if parsed.Significance >= ProactiveAlertSignificanceThreshold {
		status = "ALERT"
		// Async: generate one follow-up question/observation and store as proactive signal for FOH.
		if app != nil && app.Config() != nil {
			go runProactiveInsight(context.Background(), app, entryUUID, content)
		}
	}
	infra.LoggerFrom(ctx).Info("evaluator", "entry_uuid", entryUUID, "significance", parsed.Significance, "threshold_for_alert", ProactiveAlertSignificanceThreshold, "status", status, "domain", parsed.Domain, "fact_stored", factStored)
	_ = timestamp
	return parsed, nil
}

const proactiveInsightPrompt = `Based on this highly significant journal entry, generate exactly one insightful follow-up question or brief observation. Output only that single question or observation—no preamble, no numbering.`

func runProactiveInsight(ctx context.Context, app *infra.App, entryUUID, entryContent string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cfg := app.Config()
	if cfg == nil {
		return
	}
	userPrompt := "Entry:\n" + utils.WrapAsUserData(utils.SanitizePrompt(utils.TruncateString(entryContent, 2000)))
	summary, err := infra.GenerateContentSimple(ctx, app, proactiveInsightPrompt+prompts.DataSafety(), userPrompt, cfg, &infra.GenConfig{MaxOutputTokens: 128})
	if err != nil || summary == "" {
		if err != nil {
			infra.LoggerFrom(ctx).Debug("proactive insight LLM failed", "entry_uuid", entryUUID, "error", err)
		}
		return
	}
	bgCtx, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()
	if _, err := app.Memory.UpsertSemanticMemory(bgCtx, summary, "thought", "selfmodel", 0.9, nil, []string{entryUUID}); err != nil {
		infra.LoggerFrom(ctx).Debug("proactive insight upsert failed", "entry_uuid", entryUUID, "error", err)
		return
	}
	infra.LoggerFrom(ctx).Info("proactive insight stored", "entry_uuid", entryUUID, "preview", utils.TruncateString(summary, 60))
}
