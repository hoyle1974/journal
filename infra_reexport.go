package jot

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

// App is the application container (Firestore, Gemini, pools). Re-exported from infra.
type App = infra.App

// defaultApp is the process-wide app; set by InitDefaultApp.
var defaultApp *App

// WithApp returns a context that carries the given App.
func WithApp(ctx context.Context, app *App) context.Context {
	return infra.WithApp(ctx, app)
}

// GetApp returns the App from the context, or nil if not set.
func GetApp(ctx context.Context) *App {
	return infra.GetApp(ctx)
}

// GetDefaultApp returns the process-wide App.
func GetDefaultApp() (*App, error) {
	return infra.GetDefaultApp()
}

// InitDefaultApp initializes the process-wide App. Must be called at startup.
func InitDefaultApp(ctx context.Context) error {
	err := infra.InitDefaultApp(ctx, defaultConfig, logToGDocSync, nil)
	if err != nil {
		return err
	}
	defaultApp, _ = infra.GetDefaultApp()
	return nil
}

// NewApp creates a new App (e.g. for CLI/admin). Uses default Gemini factory; gdoc log is nil for CLI.
func NewApp(ctx context.Context, cfg *config.Config) (*App, error) {
	return infra.NewApp(ctx, cfg, nil, nil)
}

// LoggerFrom returns the logger from the App in context, or the global Logger when app is nil.
func LoggerFrom(ctx context.Context) *slog.Logger {
	return infra.LoggerFrom(ctx)
}

// StartSpan creates a new span for tracing operations.
func StartSpan(ctx context.Context, name string) (context.Context, *infra.Span) {
	return infra.StartSpan(ctx, name)
}

// WithGDocLogging returns a context that marks the caller as writing to the Google Doc log.
func WithGDocLogging(ctx context.Context) context.Context {
	return infra.WithGDocLogging(ctx)
}

// WithSyncInProgress returns a context that marks the caller as running gdoc sync.
func WithSyncInProgress(ctx context.Context) context.Context {
	return infra.WithSyncInProgress(ctx)
}

// WithForceTrace returns a context that forces the next span to be sampled and exported.
func WithForceTrace(ctx context.Context) context.Context {
	return infra.WithForceTrace(ctx)
}

// LogRequest logs an incoming HTTP request with structured fields.
func LogRequest(ctx context.Context, method, path string, statusCode int, duration time.Duration, attrs ...any) {
	infra.LogRequest(ctx, method, path, statusCode, duration, attrs...)
}

// LogOperation logs an operation with timing.
func LogOperation(ctx context.Context, operation string, duration time.Duration, err error, attrs ...any) {
	infra.LogOperation(ctx, operation, duration, err, attrs...)
}

// Span is the tracing span type. Re-exported from infra.
type Span = infra.Span

// Metric counters and types
var (
	QueriesTotal     = infra.QueriesTotal
	EntriesTotal     = infra.EntriesTotal
	ToolCallsTotal   = infra.ToolCallsTotal
	GeminiCallsTotal = infra.GeminiCallsTotal
	ErrorsTotal      = infra.ErrorsTotal
)

// NewMetricCounter creates a new counter.
func NewMetricCounter(name string) *infra.MetricCounter {
	return infra.NewMetricCounter(name)
}

// MetricCounter is the metric counter type. Re-exported from infra.
type MetricCounter = infra.MetricCounter

// GetFirestoreClient returns the Firestore client from the App in context.
func GetFirestoreClient(ctx context.Context) (*firestore.Client, error) {
	return infra.GetFirestoreClient(ctx)
}

// WrapFirestoreIndexError wraps Firestore index errors with a user-facing message.
func WrapFirestoreIndexError(err error) error {
	return infra.WrapFirestoreIndexError(err)
}

// QueryDocuments runs a Firestore query and maps each document with mapDoc.
func QueryDocuments[T any](ctx context.Context, query firestore.Query, mapDoc func(*firestore.DocumentSnapshot) (T, error)) ([]T, error) {
	return infra.QueryDocuments(ctx, query, mapDoc)
}

// GenerateUUID creates a new UUID for entries/todos.
func GenerateUUID() string {
	return infra.GenerateUUID()
}

// getStringField returns a string field from Firestore document data (wrapper for infra.GetStringField).
func getStringField(data map[string]interface{}, field string) string {
	return infra.GetStringField(data, field)
}

// getStringSliceField parses a Firestore array into []string (wrapper for infra.GetStringSliceField).
func getStringSliceField(data map[string]interface{}, field string) []string {
	return infra.GetStringSliceField(data, field)
}

// EnqueueTask creates a Cloud Task that POSTs the payload to the API at endpoint.
func EnqueueTask(ctx context.Context, endpoint string, payload map[string]interface{}) error {
	return infra.EnqueueTask(ctx, getConfig(), endpoint, payload)
}

// TwilioWebhookRequest is the incoming SMS type. Re-exported from infra.
type TwilioWebhookRequest = infra.TwilioWebhookRequest

// ValidateTwilioSignature validates that a request is from Twilio.
func ValidateTwilioSignature(r *http.Request, webhookURL string) bool {
	return infra.ValidateTwilioSignature(getConfig(), r, webhookURL)
}

// ParseTwilioWebhook parses an incoming Twilio webhook request.
func ParseTwilioWebhook(r *http.Request) (*TwilioWebhookRequest, error) {
	return infra.ParseTwilioWebhook(r)
}

// NormalizePhoneNumber normalizes a phone number to E.164 format.
func NormalizePhoneNumber(phone string) string {
	return infra.NormalizePhoneNumber(phone)
}

// IsAllowedPhoneNumber checks if the phone number is allowed.
func IsAllowedPhoneNumber(phone string) bool {
	return infra.IsAllowedPhoneNumber(getConfig(), phone)
}

// SendSMS sends an SMS via Twilio.
func SendSMS(ctx context.Context, to, body string) error {
	return infra.SendSMS(ctx, getConfig(), to, body)
}

// IsLLMQuotaOrBillingError returns true if err indicates rate limit, quota, or billing.
func IsLLMQuotaOrBillingError(err error) bool {
	return infra.IsLLMQuotaOrBillingError(err)
}

// IsLLMPermissionOrBillingDenied returns true if err indicates permission denied or billing not enabled.
func IsLLMPermissionOrBillingDenied(err error) bool {
	return infra.IsLLMPermissionOrBillingDenied(err)
}

// WrapLLMError wraps Gemini/LLM API errors with a user-facing message when applicable.
func WrapLLMError(err error) error {
	return infra.WrapLLMError(err)
}

// --- Gemini and context key re-exports (logic in pkg/infra, pkg/agent) ---

// GetGeminiClient returns the Gemini client from the App in context.
func GetGeminiClient(ctx context.Context) (*genai.Client, error) {
	return infra.GetGeminiClient(ctx)
}

// GetEffectiveModel returns the resolved model name for API calls.
func GetEffectiveModel(ctx context.Context, configured string) string {
	return infra.GetEffectiveModel(ctx, configured)
}

// GenConfig holds generation configuration options. Re-exported from infra.
type GenConfig = infra.GenConfig

// GenerateContentSimple generates content without tools.
func GenerateContentSimple(ctx context.Context, systemPrompt, userPrompt string, config *GenConfig) (string, error) {
	cfg := getConfig()
	if app := infra.GetApp(ctx); app != nil && app.Config() != nil {
		cfg = app.Config()
	}
	return infra.GenerateContentSimple(ctx, systemPrompt, userPrompt, cfg, config)
}

// EvaluateFactCollision decides whether the new fact should update or insert.
func EvaluateFactCollision(ctx context.Context, newFact, existingFact string) (string, error) {
	cfg := getConfig()
	if app := infra.GetApp(ctx); app != nil && app.Config() != nil {
		cfg = app.Config()
	}
	return infra.EvaluateFactCollision(ctx, cfg, newFact, existingFact)
}

// ExtractText extracts text content from a Gemini response.
func ExtractText(resp *genai.GenerateContentResponse) string {
	return infra.ExtractText(resp)
}

// HasFunctionCalls checks if the response contains function calls.
func HasFunctionCalls(resp *genai.GenerateContentResponse) bool {
	return infra.HasFunctionCalls(resp)
}

// ExtractFunctionCalls extracts all function calls from a response.
func ExtractFunctionCalls(resp *genai.GenerateContentResponse) []genai.FunctionCall {
	return infra.ExtractFunctionCalls(resp)
}

// EmptyResponseReason returns a short reason when the API returned no text and no function calls.
func EmptyResponseReason(resp *genai.GenerateContentResponse) string {
	return infra.EmptyResponseReason(resp)
}

// EmbedTaskRetrievalQuery and EmbedTaskRetrievalDocument are task types for embeddings.
const (
	EmbedTaskRetrievalQuery    = infra.EmbedTaskRetrievalQuery
	EmbedTaskRetrievalDocument = infra.EmbedTaskRetrievalDocument
)

// GenerateEmbedding creates a 768-dimension vector for semantic search.
func GenerateEmbedding(ctx context.Context, text string, taskType ...string) ([]float32, error) {
	cfg := getConfig()
	if app := infra.GetApp(ctx); app != nil && app.Config() != nil {
		cfg = app.Config()
	}
	projectID := ""
	if cfg != nil {
		projectID = cfg.GoogleCloudProject
	}
	return infra.GenerateEmbedding(ctx, projectID, text, taskType...)
}

// ChatSession manages a multi-turn conversation with Gemini. Re-exported from infra.
type ChatSession = infra.ChatSession

// NewChatSession creates a new chat session with tools enabled.
func NewChatSession(ctx context.Context, systemPrompt string, tools []*genai.FunctionDeclaration) (*ChatSession, error) {
	return infra.NewChatSession(ctx, systemPrompt, tools)
}

// WithCurrentEntryUUID returns a context that carries the current journal entry UUID.
func WithCurrentEntryUUID(ctx context.Context, entryUUID string) context.Context {
	return agent.WithCurrentEntryUUID(ctx, entryUUID)
}

// CurrentEntryUUIDFrom returns the current entry UUID from context, or "" if not set.
func CurrentEntryUUIDFrom(ctx context.Context) string {
	return agent.CurrentEntryUUIDFrom(ctx)
}

// SanitizePrompt and WrapAsUserData for prompts (re-exported from utils).
func SanitizePrompt(s string) string { return utils.SanitizePrompt(s) }
func WrapAsUserData(s string) string { return utils.WrapAsUserData(s) }
