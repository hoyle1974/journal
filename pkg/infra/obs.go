package infra

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	cloudtrace "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace"
	"github.com/jackstrohm/jot/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// gdocLoggingKey marks context as inside gdoc write so we don't forward those logs to gdoc again.
type gdocLoggingKeyType struct{}

var gdocLoggingKey = &gdocLoggingKeyType{}

// WithGDocLogging returns a context that marks the caller as writing to the Google Doc log.
func WithGDocLogging(ctx context.Context) context.Context {
	return context.WithValue(ctx, gdocLoggingKey, true)
}

func isGDocLogging(ctx context.Context) bool {
	return ctx.Value(gdocLoggingKey) != nil
}

// syncInProgressKey marks context as inside sync so we don't forward logs to the doc during sync.
type syncInProgressKeyType struct{}

var syncInProgressKey = &syncInProgressKeyType{}

// WithSyncInProgress returns a context that marks the caller as running gdoc sync.
func WithSyncInProgress(ctx context.Context) context.Context {
	return context.WithValue(ctx, syncInProgressKey, true)
}

func isSyncInProgress(ctx context.Context) bool {
	return ctx.Value(syncInProgressKey) != nil
}

// Logger is the global structured logger (set by InitObservability).
var Logger *slog.Logger

// LoggerFrom returns the logger from the App in context, or the global Logger when app is nil.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if app := GetApp(ctx); app != nil {
		return app.Logger
	}
	return Logger
}

var tracer trace.Tracer

var observabilityOnce sync.Once

type forceTraceKeyType struct{}

var forceTraceKey = &forceTraceKeyType{}

// WithForceTrace returns a context that forces the next span to be sampled and exported.
func WithForceTrace(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceTraceKey, true)
}

type forceTraceSampler struct {
	defaultSampler sdktrace.Sampler
}

func (s *forceTraceSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if p.ParentContext.Value(forceTraceKey) == true {
		return sdktrace.SamplingResult{
			Decision:   sdktrace.RecordAndSample,
			Tracestate: trace.SpanContextFromContext(p.ParentContext).TraceState(),
		}
	}
	return s.defaultSampler.ShouldSample(p)
}

func (s *forceTraceSampler) Description() string {
	return "forceTrace(" + s.defaultSampler.Description() + ")"
}

// InitObservability initializes logging and tracing. Must be called at startup (e.g. before InitDefaultApp).
// When cfg is nil, only a minimal logger and no-op tracer are used.
func InitObservability(cfg *config.Config) {
	observabilityOnce.Do(func() {
		initLogger(cfg)
		initTracing(cfg)
		env := "development"
		if cfg != nil && cfg.Env != "" {
			env = cfg.Env
		}
		Logger.Info("system_init", "version", Version, "commit", Commit, "env", env)
	})
}

func initLogger(cfg *config.Config) {
	levelStr := os.Getenv("LOG_LEVEL")
	var level slog.Level
	switch levelStr {
	case "info", "INFO":
		level = slog.LevelInfo
	case "warn", "WARN":
		level = slog.LevelWarn
	case "error", "ERROR":
		level = slog.LevelError
	default:
		// Default to debug so calls can be traced and the story of what/why is visible in logs.
		level = slog.LevelDebug
	}

	// In Cloud Run, stdout is captured by the platform; flush after each write so logs appear promptly.
	var logWriter io.Writer = os.Stdout
	if os.Getenv("K_SERVICE") != "" {
		logWriter = &flushAfterWriteWriter{w: bufio.NewWriter(os.Stdout)}
	}
	var baseHandler slog.Handler
	if os.Getenv("K_SERVICE") != "" {
		baseHandler = slog.NewJSONHandler(logWriter, &slog.HandlerOptions{Level: level, AddSource: true})
	} else {
		baseHandler = slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: level, AddSource: false})
	}
	// Enrich the log message with all key=value attrs so viewers that only show "message" still show the full line.
	var handler slog.Handler = &messageWithAttrsHandler{inner: baseHandler}
	if os.Getenv("K_SERVICE") != "" && cfg != nil && cfg.DocumentID != "" {
		handler = &gdocForwardingHandler{inner: handler}
	}

	env := "development"
	if cfg != nil && cfg.Env != "" {
		env = cfg.Env
	}
	Logger = slog.New(handler).With(
		slog.String("service", "jot-api"),
		slog.String("version", Version),
		slog.String("commit", Commit),
		slog.String("env", env),
	)
	slog.SetDefault(Logger)
}

// messageWithAttrsHandler wraps a handler and inlines all attributes into the log message
// so that the message string contains "msg k1=v1 k2=v2 ...". Structured attrs are preserved
// for JSON output so log viewers can still filter by field.
type messageWithAttrsHandler struct {
	inner slog.Handler
}

func (h *messageWithAttrsHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *messageWithAttrsHandler) Handle(ctx context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" ")
		b.WriteString(a.Key)
		b.WriteString("=")
		b.WriteString(a.Value.String())
		return true
	})
	// Pass only the enriched message so the inner handler doesn't print attrs again.
	enriched := slog.NewRecord(r.Time, r.Level, b.String(), 0)
	return h.inner.Handle(ctx, enriched)
}

func (h *messageWithAttrsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &messageWithAttrsHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *messageWithAttrsHandler) WithGroup(name string) slog.Handler {
	return &messageWithAttrsHandler{inner: h.inner.WithGroup(name)}
}

// flushAfterWriteWriter wraps a *bufio.Writer and flushes after each Write so Cloud Run sees each log line promptly.
type flushAfterWriteWriter struct {
	w *bufio.Writer
}

func (f *flushAfterWriteWriter) Write(p []byte) (n int, err error) {
	n, err = f.w.Write(p)
	if err != nil {
		return n, err
	}
	return n, f.w.Flush()
}

type gdocForwardingHandler struct {
	inner slog.Handler
}

func (h *gdocForwardingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *gdocForwardingHandler) Handle(ctx context.Context, r slog.Record) error {
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}
	if r.Level < slog.LevelInfo || isGDocLogging(ctx) || isSyncInProgress(ctx) {
		return nil
	}
	if r.Message == "request completed" {
		return nil
	}
	line := formatRecordForGDoc(&r)
	if line != "" {
		if app := GetApp(ctx); app != nil {
			app.SubmitGDocLog(ctx, line)
		}
	}
	return nil
}

func (h *gdocForwardingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &gdocForwardingHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *gdocForwardingHandler) WithGroup(name string) slog.Handler {
	return &gdocForwardingHandler{inner: h.inner.WithGroup(name)}
}

func formatRecordForGDoc(r *slog.Record) string {
	const maxLen = 500
	var b strings.Builder
	b.WriteString(r.Level.String())
	b.WriteString(" ")
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" ")
		b.WriteString(a.Key)
		b.WriteString("=")
		b.WriteString(a.Value.String())
		return true
	})
	s := b.String()
	if len(s) > maxLen {
		s = s[:maxLen-3] + "..."
	}
	return s
}

func initTracing(cfg *config.Config) {
	var tp *sdktrace.TracerProvider
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("jot-api"),
		semconv.ServiceVersion(Version),
	)

	if os.Getenv("K_SERVICE") != "" && cfg != nil && cfg.GoogleCloudProject != "" {
		exporter, err := cloudtrace.New(cloudtrace.WithProjectID(cfg.GoogleCloudProject))
		if err != nil {
			Logger.Error("failed to create Cloud Trace exporter", "error", err)
			tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
		} else {
			defaultSampler := sdktrace.TraceIDRatioBased(0.1)
			env := cfg.Env
			if env == "" {
				env = "production"
			}
			tp = sdktrace.NewTracerProvider(
				sdktrace.WithBatcher(exporter),
				sdktrace.WithResource(resource.NewWithAttributes(
					semconv.SchemaURL,
					semconv.ServiceName("jot-api"),
					semconv.ServiceVersion(Version),
					semconv.DeploymentEnvironment(env),
					attribute.String("cloud.project_id", cfg.GoogleCloudProject),
				)),
				sdktrace.WithSampler(&forceTraceSampler{defaultSampler: defaultSampler}),
			)
			Logger.Info("Cloud Trace exporter initialized", "project", cfg.GoogleCloudProject)
		}
	} else {
		if os.Getenv("TRACE_STDOUT") == "true" {
			exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint(), stdouttrace.WithWriter(os.Stderr))
			if err != nil {
				Logger.Error("failed to create stdout trace exporter", "error", err)
				tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
			} else {
				env := "development"
				if cfg != nil && cfg.Env != "" {
					env = cfg.Env
				}
				tp = sdktrace.NewTracerProvider(
					sdktrace.WithBatcher(exporter),
					sdktrace.WithResource(resource.NewWithAttributes(
						semconv.SchemaURL,
						semconv.ServiceName("jot-api"),
						semconv.ServiceVersion(Version),
						semconv.DeploymentEnvironment(env),
					)),
					sdktrace.WithSampler(sdktrace.AlwaysSample()),
				)
				Logger.Info("stdout trace exporter initialized")
			}
		} else {
			tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
		}
	}

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	tracer = tp.Tracer("jot-api")
}

// Span wraps an OpenTelemetry span.
type Span struct {
	span      trace.Span
	startTime time.Time
}

// StartSpan creates a new span for tracing operations.
func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	ctx, span := tracer.Start(ctx, name)
	return ctx, &Span{span: span, startTime: time.Now()}
}

// End ends the span and logs duration.
func (s *Span) End() {
	duration := time.Since(s.startTime)
	s.span.SetAttributes(attribute.Int64("duration_ms", duration.Milliseconds()))
	s.span.End()
}

// SetAttributes sets attributes on the span.
func (s *Span) SetAttributes(attrs map[string]string) {
	for k, v := range attrs {
		s.span.SetAttributes(attribute.String(k, v))
	}
}

// TraceID returns the trace ID of the span.
func (s *Span) TraceID() string {
	return s.span.SpanContext().TraceID().String()
}

// TraceIDFromContext returns the trace ID of the current span in ctx, or "" if none.
func TraceIDFromContext(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().TraceID().IsValid() {
		return ""
	}
	return span.SpanContext().TraceID().String()
}

// correlationKey holds task_id and parent_trace_id for async sub-tasks (e.g. process-entry).
type correlationKeyType struct{}

var correlationKey = &correlationKeyType{}

// Correlation holds optional IDs linking an async task to its parent request.
type Correlation struct {
	TaskID        string // ID of this async task (e.g. process-entry-abc123)
	ParentTraceID string // Trace ID of the request that enqueued this task
}

// WithCorrelation returns a context that carries the given correlation IDs for logging.
func WithCorrelation(ctx context.Context, taskID, parentTraceID string) context.Context {
	return context.WithValue(ctx, correlationKey, &Correlation{TaskID: taskID, ParentTraceID: parentTraceID})
}

// CorrelationFromContext returns the correlation from ctx, or nil if not set.
func CorrelationFromContext(ctx context.Context) *Correlation {
	c, _ := ctx.Value(correlationKey).(*Correlation)
	return c
}

// LatencyBreakdown holds per-phase durations for process-entry (and similar) requests.
type LatencyBreakdown struct {
	LLM            time.Duration
	Embedding      time.Duration
	FirestoreWrite time.Duration
	Overhead       time.Duration
}

// LogAttrs returns slog attributes for logging (e.g. on "request completed").
func (b *LatencyBreakdown) LogAttrs() []any {
	return []any{
		slog.String("latency_breakdown", b.String()),
		slog.Duration("latency_llm", b.LLM),
		slog.Duration("latency_embedding", b.Embedding),
		slog.Duration("latency_firestore_write", b.FirestoreWrite),
		slog.Duration("latency_overhead", b.Overhead),
	}
}

// String returns a short summary for logs, e.g. "llm=4.2s, embedding=0.3s, firestore_write=3.1s, overhead=2.1s".
func (b *LatencyBreakdown) String() string {
	return fmt.Sprintf("llm=%s, embedding=%s, firestore_write=%s, overhead=%s",
		b.LLM.Round(time.Millisecond), b.Embedding.Round(time.Millisecond),
		b.FirestoreWrite.Round(time.Millisecond), b.Overhead.Round(time.Millisecond))
}

type latencyBreakdownKey struct{}

// WithLatencyBreakdown attaches a latency breakdown to ctx so LogRequest can include it.
func WithLatencyBreakdown(ctx context.Context, b *LatencyBreakdown) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, &latencyBreakdownKey{}, b)
}

// LatencyBreakdownFromContext returns the latency breakdown from ctx, or nil.
func LatencyBreakdownFromContext(ctx context.Context) *LatencyBreakdown {
	b, _ := ctx.Value(&latencyBreakdownKey{}).(*LatencyBreakdown)
	return b
}

// RecordError records an error on the span.
func (s *Span) RecordError(err error) {
	if err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(codes.Error, err.Error())
		s.span.SetAttributes(
			semconv.ExceptionType(reflect.TypeOf(err).String()),
			semconv.ExceptionMessage(err.Error()),
			semconv.ExceptionStacktrace(string(debug.Stack())),
		)
	}
}

// SetStatus sets the span status.
func (s *Span) SetStatus(code codes.Code, description string) {
	s.span.SetStatus(code, description)
}

// LogRequest logs an incoming HTTP request with structured fields.
// If ctx contains a LatencyBreakdown (e.g. from process-entry), it is included as latency_breakdown and per-phase durations.
func LogRequest(ctx context.Context, method, path string, statusCode int, duration time.Duration, attrs ...any) {
	args := []any{
		slog.String("method", method),
		slog.String("path", path),
		slog.Int("status", statusCode),
		slog.Duration("duration", duration),
	}
	if b := LatencyBreakdownFromContext(ctx); b != nil {
		args = append(args, b.LogAttrs()...)
	}
	args = append(args, attrs...)
	if statusCode >= 500 {
		Logger.ErrorContext(ctx, "request completed", args...)
	} else if statusCode >= 400 {
		Logger.WarnContext(ctx, "request completed", args...)
	} else {
		Logger.InfoContext(ctx, "request completed", args...)
	}
}

// LogOperation logs an operation with timing.
func LogOperation(ctx context.Context, operation string, duration time.Duration, err error, attrs ...any) {
	args := []any{slog.String("operation", operation), slog.Duration("duration", duration)}
	args = append(args, attrs...)
	if err != nil {
		args = append(args, slog.String("error", err.Error()))
		Logger.ErrorContext(ctx, "operation failed", args...)
	} else {
		Logger.InfoContext(ctx, "operation completed", args...)
	}
}

// MetricCounter is a simple counter for metrics.
type MetricCounter struct {
	name  string
	count int64
	mu    sync.Mutex
}

// NewMetricCounter creates a new counter.
func NewMetricCounter(name string) *MetricCounter {
	return &MetricCounter{name: name}
}

// Inc increments the counter.
func (m *MetricCounter) Inc() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count++
}

// Add adds a value to the counter.
func (m *MetricCounter) Add(delta int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count += delta
}

// Value returns the current counter value.
func (m *MetricCounter) Value() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

// Application-level metrics
var (
	QueriesTotal     = NewMetricCounter("queries_total")
	EntriesTotal     = NewMetricCounter("entries_total")
	ToolCallsTotal   = NewMetricCounter("tool_calls_total")
	GeminiCallsTotal = NewMetricCounter("gemini_calls_total")
	ErrorsTotal      = NewMetricCounter("errors_total")
)
