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

// RunEvaluator assigns significance to a new entry and optionally upserts high-value facts.
func RunEvaluator(ctx context.Context, content, entryUUID, timestamp string) {
	ctx, span := StartSpan(ctx, "evaluator.run")
	defer span.End()

	if len(strings.TrimSpace(content)) < 10 {
		LoggerFrom(ctx).Info("evaluator skipped", "entry_uuid", entryUUID, "reason", "content too short")
		return
	}

	client, err := GetGeminiClient(ctx)
	if err != nil {
		LoggerFrom(ctx).Warn("evaluator skipped", "entry_uuid", entryUUID, "reason", "no gemini client", "error", err)
		return
	}

	model := client.GenerativeModel(GetEffectiveModel(ctx, GeminiModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(256)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"significance": {Type: genai.TypeNumber, Description: "0.0-1.0. High: emotional, milestones, new people. Low: routine logistics."},
			"domain":       {Type: genai.TypeString, Enum: []string{"relationship", "work", "task", "thought"}},
			"fact_to_store": {Type: genai.TypeString, Description: "Single distilled fact to store, or empty if nothing worth keeping"},
		},
	}

	systemPrompt := prompts.Evaluator() + prompts.DataSafety()
	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text(systemPrompt)}}

	// Include user_profile context so the evaluator sees prior preferences (e.g. "Jot is a hobby not work")
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
		LoggerFrom(ctx).Warn("evaluator skipped", "entry_uuid", entryUUID, "reason", "api failed", "error", err)
		return
	}

	text := extractTextFromResponse(resp)
	type evaluatorOut struct {
		Significance float64 `json:"significance"`
		Domain       string  `json:"domain"`
		FactToStore  string  `json:"fact_to_store"`
	}
	parsed, _ := llmjson.ParseLLMResponse[evaluatorOut](text, []string{"significance", "domain", "fact_to_store"})
	if parsed == nil {
		LoggerFrom(ctx).Info("evaluator skipped", "entry_uuid", entryUUID, "reason", "unparseable response")
		return
	}

	if parsed.Significance < 0 {
		parsed.Significance = 0
	}
	if parsed.Significance > 1 {
		parsed.Significance = 1
	}
	if parsed.Domain == "" {
		parsed.Domain = "thought"
	}

	// Store high-significance facts immediately
	factStored := false
	if parsed.FactToStore != "" && parsed.Significance >= 0.5 {
		nodeType := "fact"
		if parsed.Domain == "relationship" {
			nodeType = "person"
		} else if parsed.Domain == "work" {
			nodeType = "project"
		}
		if _, err := UpsertSemanticMemory(bgCtx, parsed.FactToStore, nodeType, parsed.Domain, parsed.Significance, nil, []string{entryUUID}); err != nil {
			LoggerFrom(ctx).Warn("evaluator upsert failed", "error", err)
		} else {
			factStored = true
		}
	}
	LoggerFrom(ctx).Info("evaluator", "entry_uuid", entryUUID, "significance", parsed.Significance, "domain", parsed.Domain, "fact_stored", factStored)

	// Optionally store significance on the entry for future janitor/dreamer use
	// For now we rely on semantic memory; entries stay as raw episodic log
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

// BatchCommitteeOutput is the consolidated response from the unified committee (one LLM call for all domains).
type BatchCommitteeOutput struct {
	Domains          map[string][]string `json:"domains"`           // keys: relationship, work, task, thought, selfmodel
	IdentityMarkers  []string            `json:"identity_markers"`  // permanent persona facts (PERSONA-style)
	ImpactedContexts []string            `json:"impacted_contexts"`  // context/project names mentioned
	QueryAnalysis    string              `json:"query_analysis"`     // optional: semantic clusters, knowledge gaps, curiosity trends (when recent queries provided)
}

// RunUnifiedCommittee runs a single "committee dispatch" LLM call to extract facts for all domains, identity markers, and impacted contexts.
// If recentQueries is non-empty, the committee also analyzes queries (semantic clusters, knowledge gaps, curiosity trends) and returns query_analysis.
func RunUnifiedCommittee(ctx context.Context, logs string, recentQueries string) (*BatchCommitteeOutput, error) {
	ctx, span := StartSpan(ctx, "agent.unified_committee")
	defer span.End()

	client, err := GetGeminiClient(ctx)
	if err != nil {
		return nil, WrapLLMError(err)
	}

	schemaProps := map[string]*genai.Schema{
		"relationship":      {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		"work":              {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		"task":              {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		"thought":           {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		"selfmodel":         {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		"identity_markers":   {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		"impacted_contexts": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
	}
	if recentQueries != "" {
		schemaProps["query_analysis"] = &genai.Schema{Type: genai.TypeString, Description: "Analysis of recent queries: semantic clusters, knowledge gaps, curiosity trends"}
	}

	model := client.GenerativeModel(GetEffectiveModel(ctx, DreamerModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(4096)
	model.ResponseSchema = &genai.Schema{
		Type:       genai.TypeObject,
		Properties: schemaProps,
	}
	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text(prompts.UnifiedCommittee())}}

	userPrompt := "Consolidate the last 24 hours of journal entries. Extract GOLD: people, projects, events, preferences, milestones, who is involved in what. Discard GRAVEL only: trivial one-off errands (buy milk, pick up package) with no lasting significance. For impacted_contexts: list only ongoing projects/plans/events as short snake_case names (e.g. party_planning, job_search); ignore queries and system commands.\n\nJournal logs:\n" + WrapAsUserData(SanitizePrompt(logs))
	if recentQueries != "" {
		userPrompt += "\n\nAnalyze the user's recent queries (below). (1) Group them into semantic clusters (e.g. 'Jot Development', 'Family Logistics'). (2) Identify Knowledge Gaps: What is the user asking that we couldn't answer? (3) Identify Curiosity Trends: What is the user becoming more interested in? Put the full analysis in the query_analysis field (single string).\n\nRecent queries:\n" + WrapAsUserData(SanitizePrompt(recentQueries))
	}

	apiCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	GeminiCallsTotal.Inc()
	LoggerFrom(ctx).Info("unified_committee api_call_start", "logs_len", len(logs))
	resp, err := model.GenerateContent(apiCtx, genai.Text(userPrompt))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "context deadline exceeded") {
			apiCtx2, cancel2 := context.WithTimeout(ctx, 90*time.Second)
			defer cancel2()
			resp, err = model.GenerateContent(apiCtx2, genai.Text(userPrompt))
		}
	}
	if err != nil {
		span.RecordError(err)
		return nil, WrapLLMError(err)
	}

	text := strings.TrimSpace(extractTextFromResponse(resp))
	if text == "" {
		reason := "no content in response"
		if resp != nil {
			if len(resp.Candidates) == 0 && resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != genai.BlockReasonUnspecified {
				reason = fmt.Sprintf("prompt blocked (block_reason=%s)", resp.PromptFeedback.BlockReason.String())
			} else if len(resp.Candidates) == 0 {
				reason = "no candidates returned"
			} else if len(resp.Candidates) > 0 {
				reason = fmt.Sprintf("finish_reason=%s", resp.Candidates[0].FinishReason.String())
			}
		}
		LoggerFrom(ctx).Warn("unified_committee empty response", "reason", reason)
		return nil, fmt.Errorf("unified committee returned empty response: %s (try DREAMER_UNIFIED_COMMITTEE=false to use per-domain specialists, or check model/safety settings)", reason)
	}
	type committeeParsed struct {
		Relationship     []string `json:"relationship"`
		Work             []string `json:"work"`
		Task             []string `json:"task"`
		Thought          []string `json:"thought"`
		Selfmodel        []string `json:"selfmodel"`
		IdentityMarkers  []string `json:"identity_markers"`
		ImpactedContexts []string `json:"impacted_contexts"`
		QueryAnalysis    string   `json:"query_analysis"`
	}
	committeeKeys := []string{"relationship", "work", "task", "thought", "selfmodel", "identity_markers", "impacted_contexts", "query_analysis"}
	parsed, parseErr := llmjson.ParseLLMResponse[committeeParsed](text, committeeKeys)
	if parsed == nil {
		LoggerFrom(ctx).Warn("unified_committee parse failed", "error", parseErr, "raw", truncateString(text, 500), "text_len", len(text))
		if parseErr != nil && (strings.Contains(parseErr.Error(), "unexpected end of JSON input") || len(text) < 20) {
			return nil, fmt.Errorf("unified committee returned empty or truncated response (try DREAMER_UNIFIED_COMMITTEE=false to use per-domain specialists): %w", parseErr)
		}
		return nil, fmt.Errorf("unified committee JSON parse failed: %w", parseErr)
	}

	out := &BatchCommitteeOutput{
		Domains: map[string][]string{
			"relationship": parsed.Relationship,
			"work":        parsed.Work,
			"task":        parsed.Task,
			"thought":     parsed.Thought,
			"selfmodel":   parsed.Selfmodel,
		},
		IdentityMarkers:  parsed.IdentityMarkers,
		ImpactedContexts: parsed.ImpactedContexts,
		QueryAnalysis:    strings.TrimSpace(parsed.QueryAnalysis),
	}
	LoggerFrom(ctx).Info("unified_committee done", "identity_markers", len(out.IdentityMarkers), "impacted_contexts", len(out.ImpactedContexts), "query_analysis_len", len(out.QueryAnalysis))
	return out, nil
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

	model := client.GenerativeModel(GetEffectiveModel(ctx, DreamerModel))
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

	modelName := GeminiModel
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

	model := client.GenerativeModel(GetEffectiveModel(ctx, GeminiModel))
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
