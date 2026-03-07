package infra

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
)

// Model cost per 1M tokens (input, output). Used for cost_est in logs. Approximate.
var modelCostPer1M = map[string]struct{ input, output float64 }{
	"gemini-2.5-flash":   {0.30, 2.50},
	"gemini-2.5-flash-lite": {0.10, 0.40},
	"gemini-1.5-flash":   {0.075, 0.30},
	"gemini-1.5-flash-8b": {0.0375, 0.15},
	"gemini-1.5-pro":    {1.25, 5.00},
}

// EstimateLLMCost returns an approximate cost string for the given model and token counts (e.g. "$0.000018").
func EstimateLLMCost(model string, promptTokens, completionTokens int) string {
	key := model
	if key == "" {
		key = "gemini-2.5-flash"
	}
	// Normalize model name (e.g. "models/gemini-1.5-flash" -> "gemini-1.5-flash").
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		key = key[idx+1:]
	}
	costs, ok := modelCostPer1M[key]
	if !ok {
		// Try prefix match for variants (e.g. gemini-2.5-flash-preview-05-20).
		for k, v := range modelCostPer1M {
			if strings.HasPrefix(key, k) || strings.HasPrefix(k, key) {
				costs = v
				ok = true
				break
			}
		}
	}
	if !ok {
		costs = modelCostPer1M["gemini-2.5-flash"]
	}
	in := float64(promptTokens) / 1e6 * costs.input
	out := float64(completionTokens) / 1e6 * costs.output
	return formatCost(in + out)
}

func formatCost(usd float64) string {
	if usd < 0.000001 {
		return "$0.000000"
	}
	return fmt.Sprintf("$%.6f", usd)
}

// ContextAuditTelemetry holds pre-call token breakdown and post-call actual usage for context-caching analysis.
// Static = system + tools + archive (candidate for caching). Dynamic = recent turns + current prompt.
type ContextAuditTelemetry struct {
	// Pre-call (from CountTokens)
	SystemTokens    int  // full system instruction
	ToolTokens      int  // tool definitions
	PreambleTokens  int  // system instruction text before "=======" (cacheable)
	RestOfSystemTokens int  // system instruction text after "=======" (dynamic)
	ArchiveTokens   int  // historical turns excluding the last 2
	RecentTokens    int  // last 2 turns + current user prompt
	TotalInputCalc  int  // sum of the above
	IsCacheableSize bool // true if (system + tools + archive) > 2048
	// Post-call (from UsageMetadata)
	ActualBilledPrompt  int
	ActualCandidates    int
	CachedContentTokens int // if present, implicit caching already applied
}

const contextAuditStaticThreshold = 2048

const preambleSeparator = "======="

// contentPartsToText concatenates text from genai.Parts (for system instruction, which is text-only).
func contentPartsToText(parts []genai.Part) string {
	var b strings.Builder
	for _, p := range parts {
		if t, ok := p.(genai.Text); ok {
			b.WriteString(string(t))
		}
	}
	return b.String()
}

// CollectContextAudit runs CountTokens for system, tools, archive, and recent+current to populate telemetry.
// Uses app to create a system-only model for separating system vs tool tokens. Returns nil on any CountTokens error.
func CollectContextAudit(ctx context.Context, app *App, model *genai.GenerativeModel, modelName string, history []*genai.Content, currentParts []genai.Part) *ContextAuditTelemetry {
	if app == nil || model == nil {
		return nil
	}
	client, err := app.Gemini(ctx)
	if err != nil {
		return nil
	}
	audit := &ContextAuditTelemetry{}

	// Minimal content for "preamble" counts (system, system+tools)
	emptyParts := []genai.Part{genai.Text(" ")}

	// System only: model with same system instruction, no tools
	modelSys := client.GenerativeModel(modelName)
	if model.SystemInstruction != nil {
		modelSys.SystemInstruction = model.SystemInstruction
	}
	modelSys.Tools = nil
	respSys, err := modelSys.CountTokens(ctx, emptyParts...)
	if err != nil {
		return nil
	}
	audit.SystemTokens = int(respSys.TotalTokens)

	respFull, err := model.CountTokens(ctx, emptyParts...)
	if err != nil {
		return nil
	}
	systemPlusTools := int(respFull.TotalTokens)
	if systemPlusTools >= audit.SystemTokens {
		audit.ToolTokens = systemPlusTools - audit.SystemTokens
	}

	// Preamble vs rest of system: split system instruction on "=======" and count tokens for each (content-only via plain model).
	modelPlain := client.GenerativeModel(modelName)
	modelPlain.SystemInstruction = nil
	modelPlain.Tools = nil
	if model.SystemInstruction != nil && len(model.SystemInstruction.Parts) > 0 {
		fullSystemText := contentPartsToText(model.SystemInstruction.Parts)
		if idx := strings.Index(fullSystemText, preambleSeparator); idx >= 0 {
			preambleText := strings.TrimSpace(fullSystemText[:idx])
			restText := strings.TrimSpace(fullSystemText[idx+len(preambleSeparator):])
			if preambleText != "" {
				if resp, err := modelPlain.CountTokens(ctx, genai.Text(preambleText)); err == nil {
					audit.PreambleTokens = int(resp.TotalTokens)
				}
			}
			if restText != "" {
				if resp, err := modelPlain.CountTokens(ctx, genai.Text(restText)); err == nil {
					audit.RestOfSystemTokens = int(resp.TotalTokens)
				}
			}
		} else {
			if resp, err := modelPlain.CountTokens(ctx, genai.Text(fullSystemText)); err == nil {
				audit.PreambleTokens = int(resp.TotalTokens)
			}
		}
	}

	// Archive = history excluding last 2 turns (2 turns = 4 Content items: user, model, user, model)
	const lastNContents = 4
	archiveEnd := len(history) - lastNContents
	if archiveEnd < 0 {
		archiveEnd = 0
	}
	archiveContents := history[:archiveEnd]
	recentContents := history[archiveEnd:]

	// Archive tokens: flatten archive contents to one Part list, count, subtract preamble
	if len(archiveContents) > 0 {
		var archiveParts []genai.Part
		for _, c := range archiveContents {
			if c != nil {
				archiveParts = append(archiveParts, c.Parts...)
			}
		}
		if len(archiveParts) > 0 {
			respArchive, err := model.CountTokens(ctx, archiveParts...)
			if err == nil {
				audit.ArchiveTokens = max(0, int(respArchive.TotalTokens)-systemPlusTools)
			}
		}
	}

	// Recent + current: flatten last 2 turns and current parts, count, subtract preamble
	var recentParts []genai.Part
	for _, c := range recentContents {
		if c != nil {
			recentParts = append(recentParts, c.Parts...)
		}
	}
	recentParts = append(recentParts, currentParts...)
	if len(recentParts) > 0 {
		respRecent, err := model.CountTokens(ctx, recentParts...)
		if err == nil {
			audit.RecentTokens = max(0, int(respRecent.TotalTokens)-systemPlusTools)
		}
	}

	audit.TotalInputCalc = audit.SystemTokens + audit.ToolTokens + audit.ArchiveTokens + audit.RecentTokens
	staticTotal := audit.SystemTokens + audit.ToolTokens + audit.ArchiveTokens
	audit.IsCacheableSize = staticTotal > contextAuditStaticThreshold
	return audit
}

// LogContextAudit writes a single structured log line for context-caching analysis: pre-call breakdown + actual usage.
func LogContextAudit(ctx context.Context, modelName string, audit *ContextAuditTelemetry, resp *genai.GenerateContentResponse) {
	log := LoggerFrom(ctx)
	attrs := []any{
		slog.String("event", "LLM_CONTEXT_AUDIT"),
		slog.String("message", "LLM Context Audit"),
		slog.String("model", modelName),
	}
	if audit != nil {
		attrs = append(attrs,
			slog.Int("system_tokens", audit.SystemTokens),
			slog.Int("tool_tokens", audit.ToolTokens),
			slog.Int("preamble_tokens", audit.PreambleTokens),
			slog.Int("rest_of_system_tokens", audit.RestOfSystemTokens),
			slog.Int("archive_tokens", audit.ArchiveTokens),
			slog.Int("recent_tokens", audit.RecentTokens),
			slog.Int("total_input_calc", audit.TotalInputCalc),
			slog.Bool("is_cacheable_size", audit.IsCacheableSize),
		)
	}
	var actualPrompt, actualCandidates, cachedContent int
	if resp != nil && resp.UsageMetadata != nil {
		actualPrompt = int(resp.UsageMetadata.PromptTokenCount)
		actualCandidates = int(resp.UsageMetadata.CandidatesTokenCount)
		cachedContent = int(resp.UsageMetadata.CachedContentTokenCount)
	}
	attrs = append(attrs,
		slog.Int("actual_billed_prompt", actualPrompt),
		slog.Int("actual_candidates", actualCandidates),
	)
	if cachedContent > 0 {
		attrs = append(attrs, slog.Int("cached_content_token_count", cachedContent))
	}
	msgParts := []string{"LLM_CONTEXT_AUDIT", "LLM Context Audit", "model=" + modelName,
		fmt.Sprintf("actual_billed_prompt=%d", actualPrompt)}
	if audit != nil {
		msgParts = append(msgParts,
			fmt.Sprintf("system_tokens=%d", audit.SystemTokens),
			fmt.Sprintf("tool_tokens=%d", audit.ToolTokens),
			fmt.Sprintf("preamble_tokens=%d", audit.PreambleTokens),
			fmt.Sprintf("rest_of_system_tokens=%d", audit.RestOfSystemTokens),
			fmt.Sprintf("archive_tokens=%d", audit.ArchiveTokens),
			fmt.Sprintf("recent_tokens=%d", audit.RecentTokens),
			fmt.Sprintf("total_input_calc=%d", audit.TotalInputCalc),
			fmt.Sprintf("is_cacheable_size=%v", audit.IsCacheableSize),
		)
	}
	if cachedContent > 0 {
		msgParts = append(msgParts, fmt.Sprintf("cached_content_token_count=%d", cachedContent))
	}
	log.Info(strings.Join(msgParts, " | "), attrs...)
}

// LogLLMMetrics logs a structured LLM_METRICS line after a GenerateContent/SendMessage call.
// Message includes key=value so it's visible in Cloud Logging when only the message column is shown.
func LogLLMMetrics(ctx context.Context, model string, resp *genai.GenerateContentResponse, inputSizeBytes int) {
	if resp == nil {
		return
	}
	log := LoggerFrom(ctx)
	traceID := TraceIDFromContext(ctx)
	if traceID != "" && len(traceID) > 16 {
		traceID = traceID[:16] + "..."
	}
	var promptTokens, completionTokens, totalTokens int
	if u := resp.UsageMetadata; u != nil {
		promptTokens = int(u.PromptTokenCount)
		completionTokens = int(u.CandidatesTokenCount)
		totalTokens = int(u.TotalTokenCount)
		if totalTokens == 0 {
			totalTokens = promptTokens + completionTokens
		}
	} else if inputSizeBytes > 0 {
		promptTokens = inputSizeBytes / 4
	}
	attrs := []any{
		slog.String("event", "LLM_METRICS"),
		slog.String("model", model),
		slog.Int("prompt_tokens", promptTokens),
		slog.Int("completion_tokens", completionTokens),
		slog.Int("total_tokens", totalTokens),
	}
	var msgParts []string
	msgParts = append(msgParts, "LLM_METRICS")
	if traceID != "" {
		msgParts = append(msgParts, "trace_id="+traceID)
		attrs = append(attrs, slog.String("trace_id", traceID))
	}
	msgParts = append(msgParts, "model="+model)
	if promptTokens > 0 || completionTokens > 0 {
		cost := EstimateLLMCost(model, promptTokens, completionTokens)
		attrs = append(attrs, slog.String("cost_est", cost))
		tokensStr := fmt.Sprintf("%d:%d", promptTokens, completionTokens)
		attrs = append(attrs, slog.String("tokens", tokensStr))
		msgParts = append(msgParts, "tokens="+tokensStr, "cost_est="+cost)
		if completionTokens > 0 {
			ratio := promptTokens / completionTokens
			ratioStr := fmt.Sprintf("%d:1", ratio)
			attrs = append(attrs, slog.String("overhead_ratio", ratioStr))
			msgParts = append(msgParts, "overhead_ratio="+ratioStr)
		}
	}
	msg := strings.Join(msgParts, " | ")
	log.Info(msg, attrs...)
}

// LogEmbeddingStats logs EMBEDDING_STATS for embedding API calls (dims, latency, provider).
// Message includes key=value so it's visible in Cloud Logging when only the message column is shown.
func LogEmbeddingStats(ctx context.Context, dims int, latency time.Duration) {
	latencyMs := int(latency.Milliseconds())
	msg := fmt.Sprintf("EMBEDDING_STATS | dims=%d | latency=%dms | provider=vertex-ai", dims, latencyMs)
	LoggerFrom(ctx).Debug(msg,
		slog.String("event", "EMBEDDING_STATS"),
		slog.Int("dims", dims),
		slog.Duration("latency", latency),
		slog.Int("latency_ms", latencyMs),
		slog.String("provider", "vertex-ai"),
	)
}

// LogAssistantEfficiency logs ASSISTANT_EFFICIENCY (verbosity score) for the FOH loop.
// Message includes key=value so it's visible in Cloud Logging when only the message column is shown.
func LogAssistantEfficiency(ctx context.Context, inputContextBytes, finalOutputBytes, reasoningSteps int) {
	msg := fmt.Sprintf("ASSISTANT_EFFICIENCY | input_context_size=%d | final_output_size=%d | reasoning_steps=%d",
		inputContextBytes, finalOutputBytes, reasoningSteps)
	LoggerFrom(ctx).Debug(msg,
		slog.String("event", "ASSISTANT_EFFICIENCY"),
		slog.Int("input_context_size", inputContextBytes),
		slog.Int("final_output_size", finalOutputBytes),
		slog.Int("reasoning_steps", reasoningSteps),
	)
}

// LogVectorSearchFailed logs a structured vector search failure with index, reason, and retries.
// Message includes key=value so it's visible in Cloud Logging when only the message column is shown.
func LogVectorSearchFailed(ctx context.Context, index string, err error, retries int) {
	reason := "unknown"
	errStr := ""
	if err != nil {
		errStr = err.Error()
		reason = errStr
		if strings.Contains(reason, "deadline exceeded") || strings.Contains(reason, "context deadline exceeded") {
			reason = "deadline_exceeded"
		} else if strings.Contains(reason, "not found") || strings.Contains(reason, "NotFound") {
			reason = "index_not_found"
		} else if strings.Contains(reason, "Permission denied") || strings.Contains(reason, "permission_denied") {
			reason = "permission_denied"
		}
	}
	msg := fmt.Sprintf("vector search failed | index=%s | reason=%s | retries=%d", index, reason, retries)
	attrs := []any{
		slog.String("index", index),
		slog.String("reason", reason),
		slog.Int("retries", retries),
	}
	if errStr != "" {
		attrs = append(attrs, slog.String("error", errStr))
	}
	LoggerFrom(ctx).Error(msg, attrs...)
}

// LogFoundNode logs a single found node with id, similarity score, and text preview.
// Message includes key=value so it's visible in Cloud Logging when only the message column is shown.
func LogFoundNode(ctx context.Context, id string, score float64, textPreview string) {
	msg := fmt.Sprintf("found node | id=%s | score=%.2f | text=%q", id, score, textPreview)
	LoggerFrom(ctx).Debug(msg,
		slog.String("id", id),
		slog.Float64("score", score),
		slog.String("text", textPreview),
	)
}

// LogFoundEntry logs a single found journal entry with id, similarity score, and text preview.
func LogFoundEntry(ctx context.Context, id string, score float64, textPreview string) {
	msg := fmt.Sprintf("found entry | id=%s | score=%.2f | text=%q", id, score, textPreview)
	LoggerFrom(ctx).Debug(msg,
		slog.String("id", id),
		slog.Float64("score", score),
		slog.String("text", textPreview),
	)
}

// RAG confidence status for aggregate retrieval quality.
const (
	RAGStatusHighConfidence   = "HIGH_CONFIDENCE_MATCH"
	RAGStatusMediumConfidence = "MEDIUM_CONFIDENCE_MATCH"
	RAGStatusLowConfidence    = "LOW_CONFIDENCE_MATCH"
	RAGStatusNoResults        = "NO_RESULTS"
)

// LogRAGQuality logs one aggregate RAG_QUALITY line: top_k, median and p90 similarity score, and status.
// Use this to quantify retrieval "vibe": e.g. if the system says "Logged." but status is LOW_CONFIDENCE_MATCH
// and max score was 0.30, the system is effectively flying blind.
func LogRAGQuality(ctx context.Context, topK int, scores []float64) {
	if len(scores) == 0 {
		msg := fmt.Sprintf("RAG_QUALITY | top_k=%d | median_score=N/A | p90_score=N/A | status=%s", topK, RAGStatusNoResults)
		LoggerFrom(ctx).Debug(msg,
			slog.String("event", "RAG_QUALITY"),
			slog.Int("top_k", topK),
			slog.String("status", RAGStatusNoResults),
		)
		return
	}
	sorted := make([]float64, len(scores))
	copy(sorted, scores)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 && len(sorted) >= 2 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	p90Idx := int(0.9 * float64(len(sorted)))
	if p90Idx >= len(sorted) {
		p90Idx = len(sorted) - 1
	}
	p90 := sorted[p90Idx]
	maxScore := sorted[len(sorted)-1]
	status := RAGStatusLowConfidence
	if maxScore < 0.35 {
		status = RAGStatusLowConfidence
	} else if p90 >= 0.6 {
		status = RAGStatusHighConfidence
	} else if median >= 0.5 || maxScore >= 0.6 {
		status = RAGStatusMediumConfidence
	}
	msg := fmt.Sprintf("RAG_QUALITY | top_k=%d | median_score=%.2f | p90_score=%.2f | status=%s", topK, median, p90, status)
	LoggerFrom(ctx).Debug(msg,
		slog.String("event", "RAG_QUALITY"),
		slog.Int("top_k", topK),
		slog.Float64("median_score", median),
		slog.Float64("p90_score", p90),
		slog.String("status", status),
	)
}
