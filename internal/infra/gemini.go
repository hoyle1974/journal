package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/pkg/utils"
	"golang.org/x/oauth2/google"
	"google.golang.org/genai"
)

// DefaultGeminiFactory creates a Gemini client using the built-in implementation.
// Used by NewApp when no GeminiFactory is provided.
func DefaultGeminiFactory(ctx context.Context, cfg *config.Config) (*genai.Client, string, string, error) {
	log := Logger
	if log == nil {
		log = slog.Default()
	}
	return newGeminiClientForApp(ctx, cfg, log)
}

func supportsGenerateContent(m *genai.Model) bool {
	for _, action := range m.SupportedActions {
		if action == "generateContent" || action == "GenerateContent" {
			return true
		}
	}
	// If no SupportedActions, assume model supports generateContent (e.g. from REST fallback).
	return len(m.SupportedActions) == 0
}

func modelID(m *genai.Model) string {
	if m == nil {
		return ""
	}
	return strings.TrimPrefix(m.Name, "models/")
}

func listAllModelsWithLogger(ctx context.Context, client *genai.Client, log *slog.Logger, apiKey string) (all []string, generateContent []string) {
	if log == nil {
		log = Logger
	}
	for m, err := range client.Models.All(ctx) {
		if err != nil {
			log.Warn("gemini list models iterator error", "error", err)
			break
		}
		id := modelID(m)
		if id == "" {
			continue
		}
		all = append(all, id)
		if supportsGenerateContent(m) {
			generateContent = append(generateContent, id)
		}
	}
	if len(all) == 0 && apiKey != "" {
		all = listModelsViaRESTWithLogger(ctx, log, apiKey)
		if len(all) > 0 {
			log.Info("gemini models (via REST fallback)", "models", all)
			generateContent = all
		}
	}
	return all, generateContent
}

func listModelsViaRESTWithLogger(ctx context.Context, log *slog.Logger, apiKey string) []string {
	if log == nil {
		log = Logger
	}
	url := "https://generativelanguage.googleapis.com/v1beta/models?key=" + apiKey
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Warn("gemini list models REST request failed", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Warn("gemini list models REST non-OK", "status", resp.StatusCode, "body", utils.TruncateString(string(body), 500))
		return nil
	}
	var out struct {
		Models []struct {
			Name         string   `json:"name"`
			BaseModelID  string   `json:"baseModelId"`
			Supported    []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		log.Warn("gemini list models REST decode failed", "error", err)
		return nil
	}
	ids := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		id := m.BaseModelID
		if id == "" {
			id = strings.TrimPrefix(m.Name, "models/")
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func resolveModel(configured string, available []string) string {
	use := configured
	if strings.Contains(configured, "2.5-pro") {
		use = "gemini-2.5-flash"
	}
	has := make(map[string]bool)
	for _, n := range available {
		has[n] = true
	}
	if has[use] {
		return use
	}
	for _, n := range available {
		if strings.Contains(n, "flash") {
			return n
		}
	}
	if len(available) > 0 {
		return available[0]
	}
	return use
}

func newGeminiClientForApp(ctx context.Context, cfg *config.Config, log *slog.Logger) (*genai.Client, string, string, error) {
	if cfg == nil || cfg.GeminiAPIKey == "" {
		return nil, "", "", fmt.Errorf("GEMINI_API_KEY not configured")
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  cfg.GeminiAPIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to create Gemini client: %w", err)
	}
	_, available := listAllModelsWithLogger(ctx, client, log, cfg.GeminiAPIKey)
	effGen := cfg.GeminiModel
	effDream := cfg.DreamerModel
	if len(available) > 0 {
		effGen = resolveModel(cfg.GeminiModel, available)
		effDream = resolveModel(cfg.DreamerModel, available)
		if effGen != cfg.GeminiModel || effDream != cfg.DreamerModel {
			log.Info("gemini model resolved (configured not in list)",
				"gemini_configured", cfg.GeminiModel, "gemini_resolved", effGen,
				"dreamer_configured", cfg.DreamerModel, "dreamer_resolved", effDream)
		}
	}
	if effGen == "" {
		effGen = cfg.GeminiModel
	}
	if effDream == "" {
		effDream = cfg.DreamerModel
	}
	return client, effGen, effDream, nil
}

// GetGeminiClient returns the Gemini client from the given App.
func GetGeminiClient(ctx context.Context, app *App) (*genai.Client, error) {
	if app == nil {
		return nil, fmt.Errorf("app required")
	}
	return app.Gemini(ctx)
}

// GetEffectiveModel returns the resolved model name for API calls.
func GetEffectiveModel(app *App, configured string) string {
	if app != nil {
		return app.EffectiveModel(configured)
	}
	return configured
}

// MIMETypeJSON is the MIME type for JSON responses. Use with GenConfig.ResponseMIMEType
// when requesting structured JSON from Gemini. For complex or conditional JSON, prefer
// this with prompt-driven structure (and no ResponseSchema) over genai.Schema; see .cursorrules.
const MIMETypeJSON = "application/json"

// GenConfig holds generation configuration options.
type GenConfig struct {
	Temperature      float64
	TopP             float64 // if > 0, set on model (e.g. 0.9)
	MaxOutputTokens  int
	ModelOverride    string // if non-empty, use this model
	ResponseMIMEType string // if non-empty, request JSON or other (e.g. MIMETypeJSON)
}

// GenerateContentSimple generates content without tools.
// env supplies Dispatch; pass from the caller (e.g. ToolEnv).
func GenerateContentSimple(ctx context.Context, env ToolEnv, systemPrompt, userPrompt string, cfg *config.Config, genConfig *GenConfig) (string, error) {
	ctx, span := StartSpan(ctx, "gemini.generate_simple")
	defer span.End()

	if env == nil || cfg == nil {
		return "", fmt.Errorf("env and config required")
	}
	req := &LLMRequest{
		SystemPrompt: systemPrompt,
		Parts:        []*genai.Part{{Text: userPrompt}},
		Model:        cfg.GeminiModel,
		GenConfig:    genConfig,
	}
	resp, err := env.Dispatch(ctx, req)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("gemini generation failed", "error", err)
		return "", WrapLLMError(fmt.Errorf("Gemini API error: %w", err))
	}
	text := extractTextFromResponse(resp)
	span.SetAttributes(map[string]string{"response_len": fmt.Sprintf("%d", len(text))})
	return text, nil
}

const factCollisionSystemPrompt = `You are a logic engine. Compare New Fact to Existing Fact. If they mean the exact same thing or New Fact is a direct update to Existing Fact, return 'update'. If they contradict each other or refer to different specific details, return 'insert'. If Existing Fact is empty, return 'update'. Reply with ONLY 'update' or 'insert'.`

// EvaluateFactCollision decides whether the new fact should overwrite the existing one (update) or be stored as a new node (insert).
func EvaluateFactCollision(ctx context.Context, env ToolEnv, cfg *config.Config, newFact, existingFact string) (action string, err error) {
	ctx, span := StartSpan(ctx, "gemini.evaluate_fact_collision")
	defer span.End()

	userPrompt := fmt.Sprintf("New Fact:\n%s\n\nExisting Fact:\n%s",
		utils.WrapAsUserData(newFact), utils.WrapAsUserData(existingFact))

	text, err := GenerateContentSimple(ctx, env, factCollisionSystemPrompt, userPrompt, cfg, &GenConfig{
		MaxOutputTokens: 16,
		ModelOverride:   cfg.GeminiModel,
	})
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	span.SetAttributes(map[string]string{"response": strings.TrimSpace(text)})
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(trimmed, "update") {
		return "update", nil
	}
	return "insert", nil
}

func extractTextFromResponse(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Text()
}

// ExtractText extracts text content from a Gemini response.
func ExtractText(resp *genai.GenerateContentResponse) string {
	return extractTextFromResponse(resp)
}

// EmbedTaskRetrievalQuery and EmbedTaskRetrievalDocument are task types for text-embedding-005.
const (
	EmbedTaskRetrievalQuery    = "RETRIEVAL_QUERY"
	EmbedTaskRetrievalDocument = "RETRIEVAL_DOCUMENT"
)

// GenerateEmbedding creates a 768-dimension vector for semantic search using Vertex AI text-embedding-005.
func GenerateEmbedding(ctx context.Context, projectID string, text string, taskType ...string) ([]float32, error) {
	ctx, span := StartSpan(ctx, "vertex.generate_embedding")
	defer span.End()

	task := EmbedTaskRetrievalQuery
	if len(taskType) > 0 && taskType[0] != "" {
		task = taskType[0]
	}
	inputBytes := len(text)

	endpoint := fmt.Sprintf("https://us-central1-aiplatform.googleapis.com/v1/projects/%s/locations/us-central1/publishers/google/models/text-embedding-005:predict", projectID)
	instance := map[string]interface{}{"content": text, "task_type": task}
	requestBody := map[string]interface{}{
		"instances": []map[string]interface{}{instance},
	}
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		span.RecordError(err)
		RecordEmbeddingPrometheusMetrics(task, 0, 0, inputBytes, err)
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		span.RecordError(err)
		RecordEmbeddingPrometheusMetrics(task, 0, 0, inputBytes, err)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	tokenSource, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		span.RecordError(err)
		RecordEmbeddingPrometheusMetrics(task, 0, 0, inputBytes, err)
		return nil, fmt.Errorf("failed to get token source: %w", err)
	}
	token, err := tokenSource.Token()
	if err != nil {
		span.RecordError(err)
		RecordEmbeddingPrometheusMetrics(task, 0, 0, inputBytes, err)
		return nil, fmt.Errorf("failed to get token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	client := &http.Client{Timeout: 30 * time.Second}
	embedStart := time.Now()
	resp, err := client.Do(req)
	embedLatency := time.Since(embedStart)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("embedding request failed", "error", err)
		RecordEmbeddingPrometheusMetrics(task, 0, embedLatency, inputBytes, err)
		return nil, fmt.Errorf("Embedding API error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		apiErr := fmt.Errorf("embedding API returned %d: %s", resp.StatusCode, string(body))
		span.RecordError(apiErr)
		LoggerFrom(ctx).Error("embedding failed", "status", resp.StatusCode, "body", string(body))
		RecordEmbeddingPrometheusMetrics(task, 0, embedLatency, inputBytes, apiErr)
		return nil, WrapLLMError(apiErr)
	}

	var result struct {
		Predictions []struct {
			Embeddings struct {
				Values []float32 `json:"values"`
			} `json:"embeddings"`
		} `json:"predictions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		span.RecordError(err)
		RecordEmbeddingPrometheusMetrics(task, 0, embedLatency, inputBytes, err)
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}
	if len(result.Predictions) == 0 || len(result.Predictions[0].Embeddings.Values) == 0 {
		err := fmt.Errorf("no embedding returned")
		RecordEmbeddingPrometheusMetrics(task, 0, embedLatency, inputBytes, err)
		return nil, err
	}
	dims := len(result.Predictions[0].Embeddings.Values)
	LoggerFrom(ctx).Debug("embedding generated", "dimensions", dims)
	LogEmbeddingStats(ctx, dims, embedLatency)
	RecordEmbeddingPrometheusMetrics(task, dims, embedLatency, inputBytes, nil)
	return result.Predictions[0].Embeddings.Values, nil
}
