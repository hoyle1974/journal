package jot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/llmjson"
	"golang.org/x/sync/errgroup"
)

// EvaluatorExtract holds the result of running the evaluator LLM on an entry (no storage).
type EvaluatorExtract struct {
	Significance float64
	Domain       string
	FactToStore  string
}

// RunEvaluatorExtract runs the evaluator LLM on content and returns significance, domain, and fact_to_store.
// Does not store anything. Used by backfill to decide whether and how to link an entry to knowledge nodes.
func RunEvaluatorExtract(ctx context.Context, content string) (*EvaluatorExtract, error) {
	if len(strings.TrimSpace(content)) < 10 {
		return nil, nil
	}
	client, err := GetGeminiClient(ctx)
	if err != nil {
		return nil, err
	}
	model := client.GenerativeModel(GetEffectiveModel(ctx, defaultConfig.GeminiModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(256)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"significance":  {Type: genai.TypeNumber, Description: "0.0-1.0. High: emotional, milestones, new people. Low: routine logistics."},
			"domain":        {Type: genai.TypeString, Enum: []string{"relationship", "work", "task", "thought"}},
			"fact_to_store": {Type: genai.TypeString, Description: "Single distilled fact to store, or empty if nothing worth keeping"},
		},
	}
	systemPrompt := prompts.Evaluator() + prompts.DataSafety()
	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text(systemPrompt)}}
	prompt := ""
	if node, _, err := FindContextByName(ctx, "user_profile"); err == nil && node != nil && node.Content != "" {
		prompt = fmt.Sprintf("Relevant user preferences/facts (use when assigning domain and significance):\n%s\n\n",
			truncateString(node.Content, 500))
	}
	prompt += fmt.Sprintf("Entry:\n%s", WrapAsUserData(SanitizePrompt(content)))

	bgCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	GeminiCallsTotal.Inc()
	resp, err := model.GenerateContent(bgCtx, genai.Text(prompt))
	if err != nil {
		return nil, err
	}
	text := extractTextFromResponse(resp)
	type evaluatorOut struct {
		Significance float64 `json:"significance"`
		Domain       string  `json:"domain"`
		FactToStore  string  `json:"fact_to_store"`
	}
	parsed, _ := llmjson.ParseLLMResponse[evaluatorOut](text, []string{"significance", "domain", "fact_to_store"})
	if parsed == nil {
		return nil, nil
	}
	out := &EvaluatorExtract{
		Significance: parsed.Significance,
		Domain:       parsed.Domain,
		FactToStore:  strings.TrimSpace(parsed.FactToStore),
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
	return out, nil
}

// RunEvaluator assigns significance to a new entry and optionally upserts high-value facts.
func RunEvaluator(ctx context.Context, content, entryUUID, timestamp string) {
	ctx, span := StartSpan(ctx, "evaluator.run")
	defer span.End()

	parsed, err := RunEvaluatorExtract(ctx, content)
	if err != nil {
		LoggerFrom(ctx).Warn("evaluator skipped", "entry_uuid", entryUUID, "reason", "extract failed", "error", err)
		return
	}
	if parsed == nil {
		LoggerFrom(ctx).Info("evaluator skipped", "entry_uuid", entryUUID, "reason", "content too short or unparseable")
		return
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
		if _, err := UpsertSemanticMemory(bgCtx, parsed.FactToStore, nodeType, parsed.Domain, parsed.Significance, nil, []string{entryUUID}); err != nil {
			LoggerFrom(ctx).Warn("evaluator upsert failed", "error", err)
		} else {
			factStored = true
		}
	}
	LoggerFrom(ctx).Info("evaluator", "entry_uuid", entryUUID, "significance", parsed.Significance, "domain", parsed.Domain, "fact_stored", factStored)
	_ = timestamp
}

// Domain represents a specialist's focus area.
type Domain string

const (
	DomainRelationship Domain = "relationship"
	DomainWork         Domain = "work"
	DomainTask         Domain = "task"
	DomainThought      Domain = "thought"
	DomainSelfModel    Domain = "selfmodel"
	DomainEvolution    Domain = "evolution"
)

// SpecialistInput is the payload sent to a specialist.
type SpecialistInput struct {
	UserMessage string
	Context     string
	Journal     string // Recent journal entries for context
}

// SpecialistOutput is a specialist's response.
type SpecialistOutput struct {
	Domain   Domain
	Summary  string
	Facts    []string
	Entities []string
}

// specialistSystemPrompts define each specialist's personality (loaded from internal/prompts).
var specialistSystemPrompts = map[Domain]string{
	DomainRelationship: prompts.Specialist("relationship"),
	DomainWork:          prompts.Specialist("work"),
	DomainTask:         prompts.Specialist("task"),
	DomainThought:      prompts.Specialist("thought"),
	DomainSelfModel:    prompts.Specialist("selfmodel"),
	DomainEvolution:    prompts.Specialist("evolution"),
}

// EvolutionAuditOutput is the Cognitive Engineer's nightly analysis (tool efficacy, knowledge gaps, proposals).
type EvolutionAuditOutput struct {
	Summary  string   `json:"summary"`
	Facts    []string `json:"facts"`    // specific tool/knowledge gaps
	Entities []string `json:"entities"` // proposals for new tools or features
}

// RunEvolutionAudit runs the Cognitive Engineer on recent queries (and optional journal summary) to produce system evolution recommendations.
func RunEvolutionAudit(ctx context.Context, queriesText, journalSummary string) (*EvolutionAuditOutput, error) {
	ctx, span := StartSpan(ctx, "agent.evolution_audit")
	defer span.End()

	client, err := GetGeminiClient(ctx)
	if err != nil {
		return nil, WrapLLMError(err)
	}

	model := client.GenerativeModel(GetEffectiveModel(ctx, defaultConfig.DreamerModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(2048)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":  {Type: genai.TypeString, Description: "Architectural health check: 1-3 sentences on overall system friction."},
			"facts":    {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}, Description: "Specific tool or knowledge gaps observed."},
			"entities": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}, Description: "Proposals for new tools or code changes (Go-style)."},
		},
	}
	systemPrompt := specialistSystemPrompts[DomainEvolution] + prompts.DataSafety()
	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text(systemPrompt)}}

	userPrompt := "Analyze the following user-assistant interaction log for Process Friction. Identify tool efficacy issues, knowledge gaps, and propose concrete improvements (new tools or Go code changes).\n\nRECENT QUERIES AND ANSWERS:\n" + WrapAsUserData(SanitizePrompt(queriesText))
	if journalSummary != "" {
		userPrompt += "\n\nRECENT JOURNAL THEMES (for context):\n" + WrapAsUserData(SanitizePrompt(journalSummary))
	}

	apiCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	GeminiCallsTotal.Inc()
	resp, err := model.GenerateContent(apiCtx, genai.Text(userPrompt))
	if err != nil {
		span.RecordError(err)
		return nil, WrapLLMError(err)
	}

	text := extractTextFromResponse(resp)
	out, parseErr := llmjson.ParseLLMResponse[EvolutionAuditOutput](text, []string{"summary", "facts", "entities"})
	if out == nil {
		if parseErr == nil {
			parseErr = errors.New("parse failed")
		}
		LoggerFrom(ctx).Warn("evolution_audit parse failed", "error", parseErr, "raw", truncateString(text, 400))
		return nil, fmt.Errorf("evolution audit JSON parse failed: %w", parseErr)
	}
	LoggerFrom(ctx).Info("evolution_audit done", "summary_len", len(out.Summary), "facts", len(out.Facts), "entities", len(out.Entities))
	return out, nil
}

// RunSpecialist runs a single specialist agent.
// modelOverride: if non-empty, use this model instead of GeminiModel (e.g. DreamerModel for speed).
func RunSpecialist(ctx context.Context, domain Domain, input *SpecialistInput, modelOverride string) (*SpecialistOutput, error) {
	ctx, span := StartSpan(ctx, "agent."+string(domain))
	defer span.End()

	t0 := time.Now()
	client, err := GetGeminiClient(ctx)
	clientMs := time.Since(t0).Milliseconds()
	if err != nil {
		return nil, WrapLLMError(err)
	}
	LoggerFrom(ctx).Info("specialist client_ready", "domain", domain, "client_ms", clientMs)

	prompt := fmt.Sprintf(`User message:
%s

%s`, WrapAsUserData(SanitizePrompt(input.UserMessage)), WrapAsUserData(SanitizePrompt(input.Context)))
	if input.Journal != "" {
		prompt = fmt.Sprintf(`User message:
%s

Recent journal context:
%s

%s`, WrapAsUserData(SanitizePrompt(input.UserMessage)), WrapAsUserData(SanitizePrompt(input.Journal)), WrapAsUserData(SanitizePrompt(input.Context)))
	}

	// Debug: log full prompt sent to LLM
	LoggerFrom(ctx).Info("specialist prompt", "domain", domain, "prompt_len", len(prompt), "prompt", prompt)

	modelName := defaultConfig.GeminiModel
	if modelOverride != "" {
		modelName = modelOverride
	}
	model := client.GenerativeModel(GetEffectiveModel(ctx, modelName))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(2048) // cap output to avoid degeneration loops (64k+ token runaways)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":  {Type: genai.TypeString},
			"facts":    {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"entities": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		},
	}

	systemPrompt := specialistSystemPrompts[domain] + prompts.DataSafety()
	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text(systemPrompt)}}

	// Use a 60s timeout; retry once on timeout (Gemini can be slow/intermittent)
	apiCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	GeminiCallsTotal.Inc()
	LoggerFrom(ctx).Info("specialist api_call_start", "domain", domain, "model", modelName)
	t1 := time.Now()
	resp, err := model.GenerateContent(apiCtx, genai.Text(SanitizePrompt(prompt)))
	apiMs := time.Since(t1).Milliseconds()

	// Retry once on timeout (HTTP client may wrap the error)
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "context deadline exceeded")) {
		LoggerFrom(ctx).Warn("specialist api timeout, retrying", "domain", domain, "api_ms", apiMs)
		apiCtx2, cancel2 := context.WithTimeout(ctx, 60*time.Second)
		defer cancel2()
		t1 = time.Now()
		resp, err = model.GenerateContent(apiCtx2, genai.Text(SanitizePrompt(prompt)))
		apiMs = time.Since(t1).Milliseconds()
	}

	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Warn("specialist api failed", "domain", domain, "api_ms", apiMs, "error", err)
		return nil, WrapLLMError(err)
	}
	LoggerFrom(ctx).Info("specialist api_done", "domain", domain, "api_ms", apiMs)

	text := extractTextFromResponse(resp)
	totalMs := time.Since(t0).Milliseconds()
	LoggerFrom(ctx).Info("specialist total", "domain", domain, "total_ms", totalMs, "client_ms", clientMs, "api_ms", apiMs)
	var parsed struct {
		Summary  string   `json:"summary"`
		Facts    []string `json:"facts"`
		Entities []string `json:"entities"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		LoggerFrom(ctx).Warn("specialist output parse failed", "domain", domain, "error", err, "raw", truncateString(text, 500))
		return &SpecialistOutput{Domain: domain, Summary: strings.TrimSpace(text)}, nil
	}

	if len(parsed.Facts) == 0 {
		LoggerFrom(ctx).Info("specialist returned 0 facts", "domain", domain, "summary", truncateString(parsed.Summary, 150), "raw_response", truncateString(text, 500))
	}

	return &SpecialistOutput{
		Domain:   domain,
		Summary:  parsed.Summary,
		Facts:    parsed.Facts,
		Entities: parsed.Entities,
	}, nil
}

const contextExtractorPrompt = `From the journal entries, list ongoing projects, plans, or events as short snake_case names only (e.g. party_planning, job_search, vacation_research). Ignore one-off questions and system commands. Return JSON: {"impacted_contexts": ["name1", "name2"]}.`

// RunContextExtractor uses Gemini to extract impacted_contexts (short snake_case names for active projects/plans) from the journal.
func RunContextExtractor(ctx context.Context, journalContext string) ([]string, error) {
	ctx, span := StartSpan(ctx, "agent.context_extractor")
	defer span.End()

	client, err := GetGeminiClient(ctx)
	if err != nil {
		return nil, WrapLLMError(err)
	}

	model := client.GenerativeModel(GetEffectiveModel(ctx, defaultConfig.DreamerModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(512)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"impacted_contexts": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		},
	}
	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text(contextExtractorPrompt + prompts.DataSafety())}}

	userPrompt := "Journal entries:\n" + WrapAsUserData(SanitizePrompt(journalContext))

	apiCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	GeminiCallsTotal.Inc()
	LoggerFrom(ctx).Info("context_extractor api_call_start", "journal_len", len(journalContext))
	resp, err := model.GenerateContent(apiCtx, genai.Text(userPrompt))
	if err != nil {
		span.RecordError(err)
		return nil, WrapLLMError(err)
	}

	text := strings.TrimSpace(extractTextFromResponse(resp))
	if text == "" {
		LoggerFrom(ctx).Debug("context_extractor empty response")
		return nil, nil
	}
	parsedOut, parseErr := llmjson.ParseLLMResponse[struct{ ImpactedContexts []string }](text, []string{"impacted_contexts"})
	if parseErr != nil || parsedOut == nil {
		LoggerFrom(ctx).Warn("context_extractor parse failed", "error", parseErr, "raw", truncateString(text, 300))
		return nil, nil
	}
	LoggerFrom(ctx).Info("context_extractor done", "count", len(parsedOut.ImpactedContexts))
	return parsedOut.ImpactedContexts, nil
}

const queryAnalyzerPrompt = `Analyze the user's recent queries. (1) Group them into semantic clusters (e.g. "Jot Development", "Family Logistics"). (2) Identify Knowledge Gaps: What is the user asking that we couldn't answer? (3) Identify Curiosity Trends: What is the user becoming more interested in? Return JSON with a single string field "query_analysis" containing the full analysis.`

// RunQueryAnalyzer uses Gemini to analyze recent queries for semantic clusters, knowledge gaps, and curiosity trends.
func RunQueryAnalyzer(ctx context.Context, recentQueriesText string) (string, error) {
	if strings.TrimSpace(recentQueriesText) == "" {
		return "", nil
	}

	ctx, span := StartSpan(ctx, "agent.query_analyzer")
	defer span.End()

	client, err := GetGeminiClient(ctx)
	if err != nil {
		return "", WrapLLMError(err)
	}

	model := client.GenerativeModel(GetEffectiveModel(ctx, defaultConfig.DreamerModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(1024)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"query_analysis": {Type: genai.TypeString, Description: "Analysis: semantic clusters, knowledge gaps, curiosity trends"},
		},
	}
	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text(queryAnalyzerPrompt + prompts.DataSafety())}}

	userPrompt := "Recent queries:\n" + WrapAsUserData(SanitizePrompt(recentQueriesText))

	apiCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	GeminiCallsTotal.Inc()
	LoggerFrom(ctx).Info("query_analyzer api_call_start", "queries_len", len(recentQueriesText))
	resp, err := model.GenerateContent(apiCtx, genai.Text(userPrompt))
	if err != nil {
		span.RecordError(err)
		return "", WrapLLMError(err)
	}

	text := strings.TrimSpace(extractTextFromResponse(resp))
	if text == "" {
		LoggerFrom(ctx).Debug("query_analyzer empty response")
		return "", nil
	}
	parsedOut, parseErr := llmjson.ParseLLMResponse[struct{ QueryAnalysis string }](text, []string{"query_analysis"})
	if parseErr != nil || parsedOut == nil {
		LoggerFrom(ctx).Warn("query_analyzer parse failed", "error", parseErr, "raw", truncateString(text, 300))
		return "", nil
	}
	out := strings.TrimSpace(parsedOut.QueryAnalysis)
	LoggerFrom(ctx).Info("query_analyzer done", "len", len(out))
	return out, nil
}

// DecompositionResult is the output of the decomposition step.
type DecompositionResult struct {
	Domains []Domain `json:"domains"`
}

// DecomposeMessage uses the LLM to determine which specialists to consult.
func DecomposeMessage(ctx context.Context, userMessage string) ([]Domain, error) {
	ctx, span := StartSpan(ctx, "dispatcher.decompose")
	defer span.End()

	client, err := GetGeminiClient(ctx)
	if err != nil {
		return nil, WrapLLMError(err)
	}

	model := client.GenerativeModel(GetEffectiveModel(ctx, defaultConfig.GeminiModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(512)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"domains": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeString,
					Enum: []string{"relationship", "work", "task", "thought", "selfmodel"},
				},
			},
		},
	}

	systemPrompt := prompts.Router() + prompts.DataSafety()
	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text(systemPrompt)}}

	prompt := fmt.Sprintf("User message:\n%s\n\nWhich domains to consult?", WrapAsUserData(SanitizePrompt(userMessage)))

	GeminiCallsTotal.Inc()
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		span.RecordError(err)
		return nil, WrapLLMError(err)
	}

	text := extractTextFromResponse(resp)
	var result DecompositionResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		LoggerFrom(ctx).Warn("decomposition parse failed", "error", err, "raw", truncateString(text, 200))
		return []Domain{DomainTask, DomainThought}, nil
	}

	seen := make(map[Domain]bool)
	var domains []Domain
	for _, d := range result.Domains {
		dom := Domain(d)
		if dom == DomainRelationship || dom == DomainWork || dom == DomainTask || dom == DomainThought || dom == DomainSelfModel {
			if !seen[dom] {
				seen[dom] = true
				domains = append(domains, dom)
			}
		}
	}
	if len(domains) == 0 {
		domains = []Domain{DomainTask, DomainThought}
	}
	return domains, nil
}

// RunCommittee runs the selected specialists in parallel and returns their outputs.
func RunCommittee(ctx context.Context, userMessage, journalContext string, domains []Domain) ([]*SpecialistOutput, error) {
	ctx, span := StartSpan(ctx, "dispatcher.committee")
	defer span.End()

	input := &SpecialistInput{
		UserMessage: userMessage,
		Context:     "Extract relevant facts and provide a brief summary.",
		Journal:     journalContext,
	}

	// Pre-allocate by index so output order matches input domains order (append would be completion order).
	outputs := make([]*SpecialistOutput, len(domains))
	g, gctx := errgroup.WithContext(ctx)
	for i, d := range domains {
		idx, domain := i, d
		g.Go(func() error {
			out, err := RunSpecialist(gctx, domain, input, "")
			if err != nil {
				return err
			}
			outputs[idx] = out
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		span.RecordError(err)
		return nil, WrapLLMError(err)
	}

	return outputs, nil
}
