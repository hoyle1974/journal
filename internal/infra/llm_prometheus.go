package infra

import (
	"context"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/prometheus/client_golang/prometheus"
)

const metricNamespace = "jot_llm"

var (
	llmCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "calls_total",
			Help:      "Total number of LLM API calls by model and status.",
		},
		[]string{"model", "status"},
	)
	llmRequestDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Name:      "request_duration_seconds",
			Help:      "LLM request latency in seconds.",
			Buckets:   prometheus.ExponentialBuckets(0.1, 2, 12), // 0.1s to ~205s
		},
		[]string{"model"},
	)
	llmTokensTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "tokens_total",
			Help:      "Total tokens used by model and type (prompt or completion).",
		},
		[]string{"model", "token_type"},
	)
	llmInputBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "input_bytes_total",
			Help:      "Total approximate input payload size in bytes sent to the model.",
		},
		[]string{"model"},
	)
	llmOutputBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "output_bytes_total",
			Help:      "Total output text length in bytes returned by the model.",
		},
		[]string{"model"},
	)
	llmCostEstimateUsdTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "cost_estimate_usd_total",
			Help:      "Estimated cumulative cost in USD for LLM calls by model and call_trace_id (user-facing request).",
		},
		[]string{"model", "call_trace_id"},
	)
)

const embeddingMetricNamespace = "jot_embedding"

var (
	embeddingCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: embeddingMetricNamespace,
			Name:      "calls_total",
			Help:      "Total number of embedding API calls by task_type and status.",
		},
		[]string{"task_type", "status"},
	)
	embeddingRequestDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: embeddingMetricNamespace,
			Name:      "request_duration_seconds",
			Help:      "Embedding API request latency in seconds.",
			Buckets:   prometheus.ExponentialBuckets(0.01, 2, 10), // 0.01s to ~5s
		},
		[]string{"task_type"},
	)
	embeddingInputBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: embeddingMetricNamespace,
			Name:      "input_bytes_total",
			Help:      "Total input text size in bytes sent to the embedding API by task_type.",
		},
		[]string{"task_type"},
	)
	embeddingDimensionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: embeddingMetricNamespace,
			Name:      "dimensions_total",
			Help:      "Total embedding dimensions returned (calls × dims per call) by task_type.",
		},
		[]string{"task_type"},
	)
)

func init() {
	prometheus.MustRegister(
		llmCallsTotal,
		llmRequestDurationSeconds,
		llmTokensTotal,
		llmInputBytesTotal,
		llmOutputBytesTotal,
		llmCostEstimateUsdTotal,
		embeddingCallsTotal,
		embeddingRequestDurationSeconds,
		embeddingInputBytesTotal,
		embeddingDimensionsTotal,
	)
}

// normalizeModelForLabel returns a short model name suitable for Prometheus labels (no slashes, consistent).
func normalizeModelForLabel(model string) string {
	if model == "" {
		return "unknown"
	}
	if idx := strings.LastIndex(model, "/"); idx >= 0 {
		return model[idx+1:]
	}
	return model
}

// RecordLLMPrometheusMetrics records LLM call metrics for Prometheus (and thus Google Cloud Prometheus).
// Call after each GenerateContent/SendMessage with the response and duration. Pass outputBytes from len(ExtractTextFromResponse(resp)).
// If err != nil, status is "error"; otherwise "ok". resp may be nil on error.
// call_trace_id (from TraceIDForLogging(ctx)) is recorded so cost can be graphed per request/trace.
func RecordLLMPrometheusMetrics(ctx context.Context, model string, resp *genai.GenerateContentResponse, inputSizeBytes, outputBytes int, duration time.Duration, err error) {
	label := normalizeModelForLabel(model)
	status := "ok"
	if err != nil {
		status = "error"
	}
	llmCallsTotal.WithLabelValues(label, status).Inc()
	llmRequestDurationSeconds.WithLabelValues(label).Observe(duration.Seconds())
	llmInputBytesTotal.WithLabelValues(label).Add(float64(inputSizeBytes))
	if outputBytes > 0 {
		llmOutputBytesTotal.WithLabelValues(label).Add(float64(outputBytes))
	}

	var promptTokens, completionTokens int
	if resp != nil && resp.UsageMetadata != nil {
		promptTokens = int(resp.UsageMetadata.PromptTokenCount)
		completionTokens = int(resp.UsageMetadata.CandidatesTokenCount)
	} else if inputSizeBytes > 0 {
		promptTokens = inputSizeBytes / 4
	}
	if promptTokens > 0 {
		llmTokensTotal.WithLabelValues(label, "prompt").Add(float64(promptTokens))
	}
	if completionTokens > 0 {
		llmTokensTotal.WithLabelValues(label, "completion").Add(float64(completionTokens))
	}
	costUSD := EstimateLLMCostUSD(model, promptTokens, completionTokens)
	if costUSD > 0 {
		callTraceID := TraceIDForLogging(ctx)
		if callTraceID == "" {
			callTraceID = "unknown"
		}
		llmCostEstimateUsdTotal.WithLabelValues(label, callTraceID).Add(costUSD)
	}
}

// RecordEmbeddingPrometheusMetrics records embedding API call metrics for Prometheus.
// Call from GenerateEmbedding on every exit path. taskType is e.g. RETRIEVAL_QUERY or RETRIEVAL_DOCUMENT.
// On error, dims may be 0; duration may be 0 for failures before the HTTP call.
func RecordEmbeddingPrometheusMetrics(taskType string, dims int, duration time.Duration, inputBytes int, err error) {
	if taskType == "" {
		taskType = "unknown"
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	embeddingCallsTotal.WithLabelValues(taskType, status).Inc()
	embeddingRequestDurationSeconds.WithLabelValues(taskType).Observe(duration.Seconds())
	embeddingInputBytesTotal.WithLabelValues(taskType).Add(float64(inputBytes))
	if dims > 0 {
		embeddingDimensionsTotal.WithLabelValues(taskType).Add(float64(dims))
	}
}
