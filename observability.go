package jot

import (
	"context"
	"log/slog"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	cloudtrace "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace"
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
// Used by logToGDocSync so its own log lines are not forwarded to gdoc (avoids feedback).
func WithGDocLogging(ctx context.Context) context.Context {
	return context.WithValue(ctx, gdocLoggingKey, true)
}

func isGDocLogging(ctx context.Context) bool {
	return ctx.Value(gdocLoggingKey) != nil
}

// Logger is the global structured logger for the application (used during init and when no App in context).
var Logger *slog.Logger

// LoggerFrom returns the logger from the App in context, or the global Logger when app is nil.
// Use this in request-scoped code so tests can inject a different logger via App.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if app := GetApp(ctx); app != nil {
		return app.Logger
	}
	return Logger
}

// tracer is the global tracer for distributed tracing.
var tracer trace.Tracer

var observabilityOnce sync.Once

// forceTraceKey marks context so the sampler will always export this trace (used when CLI sends X-Want-Trace-Id).
type forceTraceKeyType struct{}

var forceTraceKey = &forceTraceKeyType{}

// WithForceTrace returns a context that forces the next span to be sampled and exported.
// Used when the client sends X-Want-Trace-Id so the trace appears in Cloud Trace.
func WithForceTrace(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceTraceKey, true)
}

// forceTraceSampler delegates to a default sampler but forces RecordAndSample when context has forceTraceKey.
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

func init() {
	observabilityOnce.Do(initObservability)
}

func initObservability() {
	// Initialize structured logging
	initLogger()

	// Initialize tracing
	initTracing()
}

func initLogger() {
	// Determine log level from environment
	levelStr := os.Getenv("LOG_LEVEL")
	var level slog.Level
	switch levelStr {
	case "debug", "DEBUG":
		level = slog.LevelDebug
	case "warn", "WARN":
		level = slog.LevelWarn
	case "error", "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// Use JSON format in production (Cloud Run/Functions), text format locally
	var handler slog.Handler
	if os.Getenv("K_SERVICE") != "" {
		// Running in Cloud Run - use JSON for Cloud Logging integration
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:     level,
			AddSource: true,
		})
	} else {
		// Local development - use text format for readability
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level:     level,
			AddSource: false,
		})
	}

	// When on Cloud Run with a doc configured, also send logs to the Google Doc (request-scoped only)
	if os.Getenv("K_SERVICE") != "" && DocumentID != "" {
		handler = &gdocForwardingHandler{inner: handler}
	}

	Logger = slog.New(handler).With(
		slog.String("service", "jot-api"),
		slog.String("version", "1.0.0"),
	)

	slog.SetDefault(Logger)
}

// gdocForwardingHandler is the Google Doc "appender": it forwards each log record to the doc when context has App.
type gdocForwardingHandler struct {
	inner slog.Handler
}

func (h *gdocForwardingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *gdocForwardingHandler) Handle(ctx context.Context, r slog.Record) error {
	err := h.inner.Handle(ctx, r)
	if err != nil {
		return err
	}
	// Only forward Info and above; skip when we're inside gdoc write (avoid feedback). Use App from ctx or default so regular .Info() works.
	if r.Level < slog.LevelInfo || isGDocLogging(ctx) {
		return nil
	}
	// Skip generic request-completed lines to keep the doc focused on flow/triage
	if r.Message == "request completed" {
		return nil
	}
	line := formatRecordForGDoc(&r)
	if line != "" {
		SubmitGDocLog(ctx, line)
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

func initTracing() {
	var tp *sdktrace.TracerProvider

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("jot-api"),
		semconv.ServiceVersion("1.0.0"),
	)

	if os.Getenv("K_SERVICE") != "" && GoogleCloudProject != "" {
		// Production: use Cloud Trace exporter
		exporter, err := cloudtrace.New(cloudtrace.WithProjectID(GoogleCloudProject))
		if err != nil {
			Logger.Error("failed to create Cloud Trace exporter", "error", err)
			// Fall back to no-op
			tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
		} else {
			defaultSampler := sdktrace.TraceIDRatioBased(0.1) // Sample 10% of traces
			tp = sdktrace.NewTracerProvider(
				sdktrace.WithBatcher(exporter),
				sdktrace.WithResource(resource.NewWithAttributes(
					semconv.SchemaURL,
					semconv.ServiceName("jot-api"),
					semconv.ServiceVersion("1.0.0"),
					semconv.DeploymentEnvironment("production"),
					attribute.String("cloud.project_id", GoogleCloudProject),
				)),
				sdktrace.WithSampler(&forceTraceSampler{defaultSampler: defaultSampler}), // X-Want-Trace-Id forces export
			)
			Logger.Info("Cloud Trace exporter initialized", "project", GoogleCloudProject)
		}
	} else {
		// Local development: export to stdout (disabled by default for cleaner output)
		if os.Getenv("TRACE_STDOUT") == "true" {
			exporter, err := stdouttrace.New(
				stdouttrace.WithPrettyPrint(),
				stdouttrace.WithWriter(os.Stderr),
			)
			if err != nil {
				Logger.Error("failed to create stdout trace exporter", "error", err)
				tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
			} else {
				tp = sdktrace.NewTracerProvider(
					sdktrace.WithBatcher(exporter),
					sdktrace.WithResource(resource.NewWithAttributes(
						semconv.SchemaURL,
						semconv.ServiceName("jot-api"),
						semconv.ServiceVersion("1.0.0"),
						semconv.DeploymentEnvironment("development"),
					)),
					sdktrace.WithSampler(sdktrace.AlwaysSample()),
				)
				Logger.Info("stdout trace exporter initialized")
			}
		} else {
			// No-op tracer for local dev (cleaner output)
			tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
		}
	}

	otel.SetTracerProvider(tp)
	// W3C Trace Context so incoming/outgoing HTTP can continue the same trace.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	tracer = tp.Tracer("jot-api")
}

// Span wraps an OpenTelemetry span with convenience methods.
type Span struct {
	span      trace.Span
	startTime time.Time
}

// StartSpan creates a new span for tracing operations.
func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	ctx, span := tracer.Start(ctx, name)
	return ctx, &Span{
		span:      span,
		startTime: time.Now(),
	}
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

// TraceID returns the trace ID of the span for linking to Cloud Trace console.
func (s *Span) TraceID() string {
	return s.span.SpanContext().TraceID().String()
}

// RecordError records an error on the span with exception attributes (type, message, stacktrace)
// so traces in Cloud Trace show where the error was recorded.
func (s *Span) RecordError(err error) {
	if err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(codes.Error, err.Error())
		// Attach exception semantic attributes so backends (e.g. Cloud Trace) show stack traces.
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
func LogRequest(ctx context.Context, method, path string, statusCode int, duration time.Duration, attrs ...any) {
	args := []any{
		slog.String("method", method),
		slog.String("path", path),
		slog.Int("status", statusCode),
		slog.Duration("duration", duration),
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
	args := []any{
		slog.String("operation", operation),
		slog.Duration("duration", duration),
	}
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
