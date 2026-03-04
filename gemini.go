package jot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/llmjson"
	"golang.org/x/oauth2/google"
	"log/slog"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)


// supportsGenerateContent returns true if the model supports generateContent.
func supportsGenerateContent(m *genai.ModelInfo) bool {
	for _, method := range m.SupportedGenerationMethods {
		if method == "generateContent" || method == "GenerateContent" {
			return true
		}
	}
	return false
}

// modelID returns the id to use for GenerativeModel (BaseModelID or Name with "models/" stripped).
func modelID(m *genai.ModelInfo) string {
	if m == nil {
		return ""
	}
	if m.BaseModelID != "" {
		return m.BaseModelID
	}
	return strings.TrimPrefix(m.Name, "models/")
}

// listAllModels returns all model names and those that support generateContent (uses global Logger).
func listAllModels(ctx context.Context, client *genai.Client) (all []string, generateContent []string) {
	return listAllModelsWithLogger(ctx, client, nil)
}

// listAllModelsWithLogger is like listAllModels but uses the given logger when non-nil.
func listAllModelsWithLogger(ctx context.Context, client *genai.Client, log *slog.Logger) (all []string, generateContent []string) {
	if log == nil {
		log = Logger
	}
	it := client.ListModels(ctx)
	for {
		m, err := it.Next()
		if err != nil {
			if !errors.Is(err, iterator.Done) {
				log.Warn("gemini list models iterator error", "error", err)
			}
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
	if len(all) == 0 && GeminiAPIKey != "" {
		all = listModelsViaRESTWithLogger(ctx, log)
		if len(all) > 0 {
			log.Info("gemini models (via REST fallback)", "models", all)
			generateContent = all
		}
	}
	return all, generateContent
}

// listModelsViaREST fetches model list from GET .../v1beta/models?key= (uses global Logger).
func listModelsViaREST(ctx context.Context) []string {
	return listModelsViaRESTWithLogger(ctx, nil)
}

func listModelsViaRESTWithLogger(ctx context.Context, log *slog.Logger) []string {
	if log == nil {
		log = Logger
	}
	url := "https://generativelanguage.googleapis.com/v1beta/models?key=" + GeminiAPIKey
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
		log.Warn("gemini list models REST non-OK", "status", resp.StatusCode, "body", truncateString(string(body), 500))
		return nil
	}
	var out struct {
		Models []struct {
			Name         string   `json:"name"`
			BaseModelID string   `json:"baseModelId"`
			Supported   []string `json:"supportedGenerationMethods"`
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

// resolveModel returns preferred if it is in the list, otherwise the first model whose name contains "flash".
func resolveModel(configured string, available []string) string {
	// Never use 2.5-pro; use flash everywhere.
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

// newGeminiClientForApp creates a Gemini client and resolves model names; used by App.
// Returns (client, effectiveGeminiModel, effectiveDreamerModel, error).
func newGeminiClientForApp(ctx context.Context, log *slog.Logger) (*genai.Client, string, string, error) {
	if GeminiAPIKey == "" {
		return nil, "", "", fmt.Errorf("GEMINI_API_KEY not configured")
	}
	client, err := genai.NewClient(ctx, option.WithAPIKey(GeminiAPIKey))
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to create Gemini client: %w", err)
	}
	allModels, available := listAllModelsWithLogger(ctx, client, log)
	log.Info("gemini models (all)", "models", allModels)
	effGen := GeminiModel
	effDream := DreamerModel
	if len(available) > 0 {
		log.Info("gemini models (generateContent)", "models", available)
		effGen = resolveModel(GeminiModel, available)
		effDream = resolveModel(DreamerModel, available)
		if effGen != GeminiModel || effDream != DreamerModel {
			log.Info("gemini model resolved (configured not in list)",
				"gemini_configured", GeminiModel, "gemini_resolved", effGen,
				"dreamer_configured", DreamerModel, "dreamer_resolved", effDream)
		}
	}
	if effGen == "" {
		effGen = GeminiModel
	}
	if effDream == "" {
		effDream = DreamerModel
	}
	log.Info("gemini client initialized", "model", effGen, "dreamer_model", effDream)
	return client, effGen, effDream, nil
}

// GetGeminiClient returns the Gemini client from the App in context.
// Callers must use a context that has App attached (e.g. from an HTTP request).
func GetGeminiClient(ctx context.Context) (*genai.Client, error) {
	app := GetApp(ctx)
	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}
	return app.Gemini(ctx)
}

// GetEffectiveModel returns the resolved model name for API calls (uses list-models result to avoid 404s).
// Uses the App in context when present; otherwise returns the configured value as-is.
func GetEffectiveModel(ctx context.Context, configured string) string {
	app := GetApp(ctx)
	if app != nil {
		return app.EffectiveModel(configured)
	}
	return configured
}

// GenerateContentSimple generates content without tools (for summaries, todo detection).
func GenerateContentSimple(ctx context.Context, systemPrompt, userPrompt string, config *GenConfig) (string, error) {
	ctx, span := StartSpan(ctx, "gemini.generate_simple")
	defer span.End()

	client, err := GetGeminiClient(ctx)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	effectiveModel := GetEffectiveModel(ctx, GeminiModel)
	if config != nil && config.ModelOverride != "" {
		effectiveModel = GetEffectiveModel(ctx, config.ModelOverride)
	}
	model := client.GenerativeModel(effectiveModel)

	if systemPrompt != "" {
		model.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(SanitizePrompt(systemPrompt))},
		}
	}

	if config != nil {
		if config.Temperature > 0 {
			model.SetTemperature(float32(config.Temperature))
		}
		if config.MaxOutputTokens > 0 {
			model.SetMaxOutputTokens(int32(config.MaxOutputTokens))
		}
		if config.ResponseMIMEType != "" {
			model.ResponseMIMEType = config.ResponseMIMEType
		}
	}

	span.SetAttributes(map[string]string{
		"model":       effectiveModel,
		"prompt_len":  fmt.Sprintf("%d", len(userPrompt)),
		"has_system":  fmt.Sprintf("%t", systemPrompt != ""),
	})

	resp, err := model.GenerateContent(ctx, genai.Text(SanitizePrompt(userPrompt)))
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("gemini generation failed", "error", err)
		return "", WrapLLMError(fmt.Errorf("Gemini API error: %w", err))
	}

	text := extractTextFromResponse(resp)
	span.SetAttributes(map[string]string{"response_len": fmt.Sprintf("%d", len(text))})

	return text, nil
}

// GenConfig holds generation configuration options.
type GenConfig struct {
	Temperature     float64
	MaxOutputTokens int
	// ModelOverride: if non-empty, use this model (e.g. DreamerModel for dream pipeline consistency).
	ModelOverride string
	// ResponseMIMEType: if non-empty, request JSON (or other) response from the model.
	ResponseMIMEType string
}

const factCollisionSystemPrompt = `You are a logic engine. Compare New Fact to Existing Fact. If they mean the exact same thing or New Fact is a direct update to Existing Fact, return 'update'. If they contradict each other or refer to different specific details, return 'insert'. If Existing Fact is empty, return 'update'. Reply with ONLY 'update' or 'insert'.`

// EvaluateFactCollision decides whether the new fact should overwrite the existing one (update) or be stored as a new node (insert).
// Used to avoid semantic collisions when vector similarity is high but meanings differ (e.g. "Jack likes X" vs "Jack hates X").
// On LLM error, callers should default to "insert" to avoid data loss.
func EvaluateFactCollision(ctx context.Context, newFact, existingFact string) (action string, err error) {
	ctx, span := StartSpan(ctx, "gemini.evaluate_fact_collision")
	defer span.End()

	userPrompt := fmt.Sprintf("New Fact:\n%s\n\nExisting Fact:\n%s",
		WrapAsUserData(newFact), WrapAsUserData(existingFact))

	text, err := GenerateContentSimple(ctx, factCollisionSystemPrompt, userPrompt, &GenConfig{
		MaxOutputTokens: 16,
		ModelOverride:   GeminiModel,
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

// llmQuotaBillingKeywords match Gemini/Google API error messages for rate limit, quota, or billing.
var llmQuotaBillingKeywords = []string{
	"429", "resource_exhausted", "RESOURCE_EXHAUSTED", "quota", "rate limit", "rate_limit",
	"billing", "exceeded", "limit exceeded", "daily limit", "per minute",
}

// llmPermissionKeywords match permission/entitlement errors (e.g. API key, billing not enabled).
var llmPermissionKeywords = []string{
	"403", "permission_denied", "PERMISSION_DENIED", "forbidden", "invalid api key",
	"api key not valid", "billing has not been enabled", "FAILED_PRECONDITION",
}

// IsLLMQuotaOrBillingError returns true if err indicates rate limit, quota, or billing (callers may return HTTP 429).
func IsLLMQuotaOrBillingError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, k := range llmQuotaBillingKeywords {
		if strings.Contains(s, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// IsLLMPermissionOrBillingDenied returns true if err indicates permission denied or billing not enabled (callers may return HTTP 403).
func IsLLMPermissionOrBillingDenied(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, k := range llmPermissionKeywords {
		if strings.Contains(s, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// userFacingMessageForLLMError returns a short message for the user, or "" if not a known LLM API error.
func userFacingMessageForLLMError(err error) string {
	if err == nil {
		return ""
	}
	if IsLLMPermissionOrBillingDenied(err) {
		return "Permission denied or billing not enabled. Verify your API key and that billing is enabled for the Gemini API."
	}
	if IsLLMQuotaOrBillingError(err) {
		return "Rate limit or billing limit exceeded. Check your quota at Google AI Studio (aistudio.google.com) or Cloud Console, and try again later."
	}
	return ""
}

// WrapLLMError wraps Gemini/LLM API errors with a user-facing message when applicable (quota, billing, permission).
// Callers should return this so the user sees a clear message instead of raw API text.
func WrapLLMError(err error) error {
	if err == nil {
		return nil
	}
	msg := userFacingMessageForLLMError(err)
	if msg == "" {
		return err
	}
	return fmt.Errorf("%s — %w", msg, err)
}

// extractTextFromResponse extracts text content from a Gemini response.
func extractTextFromResponse(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return ""
	}

	candidate := resp.Candidates[0]
	if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
		return ""
	}

	for _, part := range candidate.Content.Parts {
		if text, ok := part.(genai.Text); ok {
			return string(text)
		}
	}

	return ""
}

const rerankSystemPrompt = "You are a re-ranker. Given a query and a numbered list of text items, return a JSON array of the item numbers (1-based indices) that best answer the query, ordered by relevance. Only include relevant items. Example: [3, 1, 5]."

// RerankNodes uses the LLM to re-rank knowledge nodes by relevance to the query.
// Returns at most topN nodes in the order returned by the model. On LLM or parse error, returns the first topN of the input.
func RerankNodes(ctx context.Context, query string, nodes []KnowledgeNode, topN int) ([]KnowledgeNode, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	if topN <= 0 {
		topN = len(nodes)
	}

	var sb strings.Builder
	for i, n := range nodes {
		content := n.Content
		if n.Metadata != "" && n.Metadata != "{}" {
			content = content + " " + n.Metadata
		}
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, content))
	}
	userPrompt := fmt.Sprintf("Query: %s\n\nNumbered items:\n%s\nReturn a JSON array of the 1-based indices that best answer the query, ordered by relevance (e.g. [2, 5, 1]).", query, sb.String())

	jsonText, err := GenerateContentSimple(ctx, rerankSystemPrompt, userPrompt, &GenConfig{
		ResponseMIMEType: "application/json",
		MaxOutputTokens:  512,
	})
	if err != nil {
		LoggerFrom(ctx).Warn("rerank generation failed, using first topN", "error", err)
		return firstN(nodes, topN), nil
	}

	// Parse as []int or []float64 (Gemini may return numbers as floats)
	var indices []int
	if err := llmjson.RepairAndUnmarshal(jsonText, &indices); err != nil {
		var floats []float64
		if err2 := llmjson.RepairAndUnmarshal(jsonText, &floats); err2 != nil {
			LoggerFrom(ctx).Warn("rerank parse failed, using first topN", "error", err, "error2", err2)
			return firstN(nodes, topN), nil
		}
		indices = make([]int, 0, len(floats))
		for _, f := range floats {
			indices = append(indices, int(f))
		}
	}

	seen := make(map[int]bool)
	var result []KnowledgeNode
	for _, idx := range indices {
		if len(result) >= topN {
			break
		}
		// 1-based index -> nodes[i-1]
		if idx < 1 || idx > len(nodes) || seen[idx] {
			continue
		}
		seen[idx] = true
		result = append(result, nodes[idx-1])
	}
	if len(result) == 0 {
		return firstN(nodes, topN), nil
	}
	return result, nil
}

func firstN(nodes []KnowledgeNode, n int) []KnowledgeNode {
	if n <= 0 || len(nodes) == 0 {
		return nil
	}
	if n >= len(nodes) {
		return nodes
	}
	out := make([]KnowledgeNode, n)
	copy(out, nodes[:n])
	return out
}

// ChatSession manages a multi-turn conversation with Gemini.
type ChatSession struct {
	model   *genai.GenerativeModel
	session *genai.ChatSession
	ctx     context.Context
}

// NewChatSession creates a new chat session with tools enabled.
func NewChatSession(ctx context.Context, systemPrompt string, tools []*genai.FunctionDeclaration) (*ChatSession, error) {
	ctx, span := StartSpan(ctx, "gemini.new_chat_session")
	defer span.End()

	client, err := GetGeminiClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	effectiveModel := GetEffectiveModel(ctx, GeminiModel)
	model := client.GenerativeModel(effectiveModel)

	if systemPrompt != "" {
		model.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(SanitizePrompt(systemPrompt))},
		}
	}

	if len(tools) > 0 {
		model.Tools = []*genai.Tool{{
			FunctionDeclarations: tools,
		}}
	}

	session := model.StartChat()

	LoggerFrom(ctx).Debug("chat session created",
		"model", effectiveModel,
		"tools_count", len(tools),
		"has_system", systemPrompt != "",
	)

	return &ChatSession{
		model:   model,
		session: session,
		ctx:     ctx,
	}, nil
}

// sanitizeParts ensures all text in parts is valid UTF-8 before sending to Gemini.
func sanitizeParts(parts []genai.Part) []genai.Part {
	out := make([]genai.Part, len(parts))
	for i, part := range parts {
		switch p := part.(type) {
		case genai.Text:
			out[i] = genai.Text(SanitizePrompt(string(p)))
		case genai.FunctionResponse:
			sanitizedResp := make(map[string]any)
			for k, v := range p.Response {
				if s, ok := v.(string); ok {
					sanitizedResp[k] = SanitizePrompt(s)
				} else {
					sanitizedResp[k] = v
				}
			}
			out[i] = genai.FunctionResponse{Name: p.Name, Response: sanitizedResp}
		default:
			out[i] = part
		}
	}
	return out
}

// SendMessage sends a message and returns the response.
func (cs *ChatSession) SendMessage(ctx context.Context, parts ...genai.Part) (*genai.GenerateContentResponse, error) {
	ctx, span := StartSpan(ctx, "gemini.send_message")
	defer span.End()

	sanitized := sanitizeParts(parts)
	resp, err := cs.session.SendMessage(ctx, sanitized...)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("chat message failed", "error", err)
		return nil, fmt.Errorf("Gemini chat error: %w", err)
	}

	return resp, nil
}

// AddFunctionResponse adds a function response to the conversation history.
func (cs *ChatSession) AddFunctionResponse(name string, response map[string]any) genai.Part {
	return genai.FunctionResponse{
		Name:     name,
		Response: response,
	}
}

// GetHistory returns the current conversation history.
func (cs *ChatSession) GetHistory() []*genai.Content {
	return cs.session.History
}

// TrimHistory keeps only the last n message pairs in history.
func (cs *ChatSession) TrimHistory(maxPairs int) {
	history := cs.session.History
	maxMessages := maxPairs * 2

	if len(history) > maxMessages {
		cs.session.History = history[len(history)-maxMessages:]
		LoggerFrom(cs.ctx).Debug("chat history trimmed",
			"from", len(history),
			"to", len(cs.session.History),
		)
	}
}

// HasFunctionCalls checks if the response contains function calls.
func HasFunctionCalls(resp *genai.GenerateContentResponse) bool {
	if resp == nil || len(resp.Candidates) == 0 {
		return false
	}

	candidate := resp.Candidates[0]
	if candidate.Content == nil {
		return false
	}

	for _, part := range candidate.Content.Parts {
		if _, ok := part.(genai.FunctionCall); ok {
			return true
		}
	}

	return false
}

// ExtractFunctionCalls extracts all function calls from a response.
func ExtractFunctionCalls(resp *genai.GenerateContentResponse) []genai.FunctionCall {
	var calls []genai.FunctionCall

	if resp == nil || len(resp.Candidates) == 0 {
		return calls
	}

	candidate := resp.Candidates[0]
	if candidate.Content == nil {
		return calls
	}

	for _, part := range candidate.Content.Parts {
		if fc, ok := part.(genai.FunctionCall); ok {
			calls = append(calls, fc)
		}
	}

	return calls
}

// ExtractText extracts text content from a response.
func ExtractText(resp *genai.GenerateContentResponse) string {
	return extractTextFromResponse(resp)
}

// EmptyResponseReason returns a short reason string when the API returned no text and no function calls (for user-facing errors).
func EmptyResponseReason(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return "No response from API."
	}
	if len(resp.Candidates) == 0 {
		if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != genai.BlockReasonUnspecified {
			return fmt.Sprintf("Prompt blocked (%s).", resp.PromptFeedback.BlockReason.String())
		}
		return "No candidates returned."
	}
	c := resp.Candidates[0]
	if c.Content == nil || len(c.Content.Parts) == 0 {
		return fmt.Sprintf("Empty content (finish_reason=%s).", c.FinishReason.String())
	}
	return fmt.Sprintf("Finish reason: %s.", c.FinishReason.String())
}

// Task types for text-embedding-005 (improves retrieval quality)
const (
	EmbedTaskRetrievalQuery    = "RETRIEVAL_QUERY"    // for search queries
	EmbedTaskRetrievalDocument = "RETRIEVAL_DOCUMENT" // for documents being indexed
)

// GenerateEmbedding creates a 768-dimension vector for semantic search using Vertex AI text-embedding-005.
// taskType: RETRIEVAL_QUERY for search queries, RETRIEVAL_DOCUMENT for documents (default: RETRIEVAL_QUERY).
func GenerateEmbedding(ctx context.Context, text string, taskType ...string) ([]float32, error) {
	ctx, span := StartSpan(ctx, "vertex.generate_embedding")
	defer span.End()

	task := EmbedTaskRetrievalQuery
	if len(taskType) > 0 && taskType[0] != "" {
		task = taskType[0]
	}

	// Use Vertex AI text-embedding-005 via REST API (768 dims, same as 004)
	endpoint := fmt.Sprintf("https://us-central1-aiplatform.googleapis.com/v1/projects/%s/locations/us-central1/publishers/google/models/text-embedding-005:predict", GoogleCloudProject)

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

	// Get access token from default credentials
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
	resp, err := client.Do(req)
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

	LoggerFrom(ctx).Debug("embedding generated", "dimensions", len(result.Predictions[0].Embeddings.Values))
	return result.Predictions[0].Embeddings.Values, nil
}
