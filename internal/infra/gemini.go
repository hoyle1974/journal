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
func DefaultGeminiFactory(ctx context.Context, cfg *config.Config) (*genai.Client, string, error) {
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

func newGeminiClientForApp(ctx context.Context, cfg *config.Config, log *slog.Logger) (*genai.Client, string, error) {
	if cfg == nil || cfg.GeminiAPIKey == "" {
		return nil, "", fmt.Errorf("GEMINI_API_KEY not configured")
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  cfg.GeminiAPIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to create Gemini client: %w", err)
	}
	_, available := listAllModelsWithLogger(ctx, client, log, cfg.GeminiAPIKey)
	effGen := cfg.GeminiModel
	if len(available) > 0 {
		effGen = resolveModel(cfg.GeminiModel, available)
		if effGen != cfg.GeminiModel {
			log.Info("gemini model resolved (configured not in list)",
				"gemini_configured", cfg.GeminiModel, "gemini_resolved", effGen)
		}
	}
	if effGen == "" {
		effGen = cfg.GeminiModel
	}
	return client, effGen, nil
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
	text := strings.TrimSpace(extractTextFromResponse(resp))
	span.SetAttributes(map[string]string{"response_len": fmt.Sprintf("%d", len(text))})
	return text, nil
}

const imageCaptionSystemPrompt = `You are describing an image for a personal journal entry. Output a short, factual description of what is in the image: people, place, objects, activity, text visible in the image, or "No clear content" if you cannot describe it. One or two sentences only. No preamble or "The image shows".`

// GenerateImageCaption uses a vision model to describe the image for journaling.
// imageBytes and mimeType are the image data (e.g. from Telegram). userCaption is optional text the user sent with the image.
// Returns a combined string: userCaption + auto-generated description, suitable for the journal entry and FOH.
func GenerateImageCaption(ctx context.Context, env ToolEnv, imageBytes []byte, mimeType, userCaption string, cfg *config.Config) (string, error) {
	ctx, span := StartSpan(ctx, "gemini.generate_image_caption")
	defer span.End()

	if env == nil || cfg == nil {
		return "", fmt.Errorf("env and config required")
	}
	if len(imageBytes) == 0 {
		return "", fmt.Errorf("image bytes required")
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	imagePart := genai.NewPartFromBytes(imageBytes, mimeType)
	if imagePart == nil {
		return "", fmt.Errorf("failed to create image part")
	}
	parts := []*genai.Part{imagePart}
	if strings.TrimSpace(userCaption) != "" {
		parts = append(parts, &genai.Part{Text: "User caption: " + utils.WrapAsUserData(userCaption) + "\nInclude this in your description if relevant."})
	}
	req := &LLMRequest{
		SystemPrompt: imageCaptionSystemPrompt,
		Parts:        parts,
		Model:        cfg.GeminiModel,
		GenConfig:    &GenConfig{MaxOutputTokens: 256},
	}
	resp, err := env.Dispatch(ctx, req)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("image caption generation failed", "error", err)
		return "", WrapLLMError(fmt.Errorf("Gemini image caption: %w", err))
	}
	generated := strings.TrimSpace(extractTextFromResponse(resp))
	span.SetAttributes(map[string]string{"caption_len": fmt.Sprintf("%d", len(generated))})
	// Combine user caption (if any) and generated description for journal and FOH.
	var combined strings.Builder
	if strings.TrimSpace(userCaption) != "" {
		combined.WriteString(strings.TrimSpace(userCaption))
		combined.WriteString("\n")
	}
	combined.WriteString(generated)
	return combined.String(), nil
}

const audioTranscriptionSystemPrompt = `You are a speech-to-text transcription engine. Transcribe the audio exactly as spoken. Output only the verbatim transcript with no commentary, labels, or preamble. If the audio is silent or unintelligible, output exactly: [inaudible]`

// TranscribeAudio sends audio bytes to Gemini and returns the verbatim transcript.
// audioBytes must be audio/ogg (Telegram voice note format).
// Returns an error if transcription fails; returns "[inaudible]" (not an error) for silent/unclear audio.
func TranscribeAudio(ctx context.Context, env ToolEnv, audioBytes []byte, cfg *config.Config) (string, error) {
	ctx, span := StartSpan(ctx, "gemini.transcribe_audio")
	defer span.End()

	if env == nil || cfg == nil {
		return "", fmt.Errorf("env and config required")
	}
	if len(audioBytes) == 0 {
		return "", fmt.Errorf("audio bytes required")
	}
	audioPart := genai.NewPartFromBytes(audioBytes, "audio/ogg")
	if audioPart == nil {
		return "", fmt.Errorf("failed to create audio part")
	}
	req := &LLMRequest{
		SystemPrompt: audioTranscriptionSystemPrompt,
		Parts:        []*genai.Part{audioPart},
		Model:        cfg.GeminiModel,
		GenConfig:    &GenConfig{MaxOutputTokens: 1024},
	}
	resp, err := env.Dispatch(ctx, req)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("audio transcription failed", "error", err)
		return "", WrapLLMError(fmt.Errorf("Gemini transcription: %w", err))
	}
	transcript := strings.TrimSpace(extractTextFromResponse(resp))
	span.SetAttributes(map[string]string{"transcript_len": fmt.Sprintf("%d", len(transcript))})
	if transcript == "" {
		transcript = "[inaudible]"
	}
	return transcript, nil
}

func extractTextFromResponse(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Text()
}

// EmbedTaskRetrievalQuery is the task type for retrieval queries (text-embedding-005).
const EmbedTaskRetrievalQuery = "RETRIEVAL_QUERY"

// GenerateEmbedding creates a 768-dimension vector for semantic search using Vertex AI text-embedding-005.
func GenerateEmbedding(ctx context.Context, projectID string, text string, taskType ...string) ([]float32, error) {
	ctx, span := StartSpan(ctx, "vertex.generate_embedding")
	defer span.End()

	task := EmbedTaskRetrievalQuery
	if len(taskType) > 0 && taskType[0] != "" {
		task = taskType[0]
	}

	endpoint := fmt.Sprintf("https://us-central1-aiplatform.googleapis.com/v1/projects/%s/locations/us-central1/publishers/google/models/text-embedding-005:predict", projectID)
	instance := map[string]interface{}{"content": text, "task_type": task}
	requestBody := map[string]interface{}{
		"instances": []map[string]interface{}{instance},
	}
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	tokenSource, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get token source: %w", err)
	}
	token, err := tokenSource.Token()
	if err != nil {
		span.RecordError(err)
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
		return nil, fmt.Errorf("Embedding API error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		apiErr := fmt.Errorf("embedding API returned %d: %s", resp.StatusCode, string(body))
		span.RecordError(apiErr)
		LoggerFrom(ctx).Error("embedding failed", "status", resp.StatusCode, "body", string(body))
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
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}
	if len(result.Predictions) == 0 || len(result.Predictions[0].Embeddings.Values) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	dims := len(result.Predictions[0].Embeddings.Values)
	LoggerFrom(ctx).Debug("embedding generated", "dimensions", dims)
	LogEmbeddingStats(ctx, dims, embedLatency)
	return result.Predictions[0].Embeddings.Values, nil
}
