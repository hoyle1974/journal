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
