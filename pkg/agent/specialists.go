package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/llmjson"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"golang.org/x/sync/errgroup"
)

// ProactiveAlertSignificanceThreshold is the minimum significance for an entry to be
// considered for a proactive alert (e.g. selfmodel thought). Logged so you can see
// how close low-scoring entries came (e.g. "I feel dizzy" at 0.2 → tune evaluator for health).
const ProactiveAlertSignificanceThreshold = 0.8

// EvaluatorExtract holds the result of running the evaluator LLM on an entry (no storage).
type EvaluatorExtract struct {
	Significance      float64
	Domain            string
	FactToStore       string
	FutureCommitment  float64 // 0-1: extent to which the entry expresses a commitment to do something
	CommitmentIntent  string  // one sentence describing the action, if future_commitment is high
}

// RunEvaluatorExtract runs the evaluator LLM on content and returns significance, domain, and fact_to_store.
func RunEvaluatorExtract(ctx context.Context, content string) (*EvaluatorExtract, error) {
	if len(strings.TrimSpace(content)) < 10 {
		return nil, nil
	}
	app := infra.GetApp(ctx)
	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}
	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"significance":       {Type: genai.TypeNumber, Description: "0.0-1.0. High: emotional, milestones, new people. Low: routine logistics."},
			"domain":             {Type: genai.TypeString, Enum: []string{"relationship", "work", "task", "thought"}},
			"fact_to_store":      {Type: genai.TypeString, Description: "Single distilled fact to store, or empty if nothing worth keeping"},
			"future_commitment":  {Type: genai.TypeNumber, Description: "0.0-1.0. High when entry expresses something user will do or needs to do (I need to..., I will...)."},
			"commitment_intent":  {Type: genai.TypeString, Description: "One sentence describing the action to take, or empty if no commitment."},
		},
	}
	systemPrompt := prompts.Evaluator() + prompts.DataSafety()
	prompt := ""
	node, _, err := memory.FindContextByName(ctx, "user_profile")
	if err == nil && node != nil && node.Content != "" {
		profile := node.Content
		prompt = fmt.Sprintf("Relevant user preferences/facts (use when assigning domain and significance):\n%s\n\n",
			utils.TruncateString(profile, 500))
	}
	prompt += fmt.Sprintf("Entry:\n%s", utils.WrapAsUserData(utils.SanitizePrompt(content)))

	bgCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req := &infra.LLMRequest{
		SystemPrompt:   systemPrompt,
		Parts:          []*genai.Part{{Text: prompt}},
		Model:          app.Config().GeminiModel,
		GenConfig:      &infra.GenConfig{MaxOutputTokens: 1024, ResponseMIMEType: infra.MIMETypeJSON},
		ResponseSchema: schema,
	}
	infra.GeminiCallsTotal.Inc()
	resp, err := app.Dispatch(bgCtx, req)
	if err != nil {
		return nil, err
	}
	text := infra.ExtractTextFromResponse(resp)
	type evaluatorOut struct {
		Significance      float64 `json:"significance"`
		Domain            string  `json:"domain"`
		FactToStore       string  `json:"fact_to_store"`
		FutureCommitment  float64 `json:"future_commitment"`
		CommitmentIntent  string  `json:"commitment_intent"`
	}
	parsed, _ := llmjson.ParseLLMResponse[evaluatorOut](text, []string{"significance", "domain", "fact_to_store", "future_commitment", "commitment_intent"})
	if parsed == nil {
		return nil, nil
	}
	out := &EvaluatorExtract{
		Significance:     parsed.Significance,
		Domain:           parsed.Domain,
		FactToStore:      strings.TrimSpace(parsed.FactToStore),
		FutureCommitment: parsed.FutureCommitment,
		CommitmentIntent: strings.TrimSpace(parsed.CommitmentIntent),
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
	if out.FutureCommitment < 0 {
		out.FutureCommitment = 0
	}
	if out.FutureCommitment > 1 {
		out.FutureCommitment = 1
	}
	return out, nil
}

// AgencyTaskCommitmentThreshold is the minimum future_commitment score to auto-create a task from an entry.
const AgencyTaskCommitmentThreshold = 0.6

// MinCommitmentIntentLen is the minimum length of commitment_intent to auto-create a task (avoid vague commitments).
const MinCommitmentIntentLen = 10

// RunEvaluator assigns significance to a new entry, optionally upserts high-value facts, and returns the extract for agency (task creation).
func RunEvaluator(ctx context.Context, app *infra.App, content, entryUUID, timestamp string) (*EvaluatorExtract, error) {
	ctx, span := infra.StartSpan(ctx, "evaluator.run")
	defer span.End()

	parsed, err := RunEvaluatorExtract(ctx, content)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("evaluator skipped", "entry_uuid", entryUUID, "reason", "extract failed", "error", err)
		return nil, err
	}
	if parsed == nil {
		infra.LoggerFrom(ctx).Info("evaluator skipped", "entry_uuid", entryUUID, "reason", "content too short or unparseable")
		return nil, nil
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
		if _, err := memory.UpsertSemanticMemory(bgCtx, parsed.FactToStore, nodeType, parsed.Domain, parsed.Significance, nil, []string{entryUUID}); err != nil {
			infra.LoggerFrom(ctx).Warn("evaluator upsert failed", "error", err)
		} else {
			factStored = true
		}
	}
	status := "IGNORE_PROACTIVE"
	if parsed.Significance >= ProactiveAlertSignificanceThreshold {
		status = "ALERT"
		// Async: generate one follow-up question/observation and store as proactive signal for FOH.
		if app != nil && app.Config() != nil {
			go runProactiveInsight(context.Background(), app, entryUUID, content)
		}
	}
	infra.LoggerFrom(ctx).Info("evaluator", "entry_uuid", entryUUID, "significance", parsed.Significance, "threshold_for_alert", ProactiveAlertSignificanceThreshold, "status", status, "domain", parsed.Domain, "fact_stored", factStored)
	_ = timestamp
	return parsed, nil
}

const proactiveInsightPrompt = `Based on this highly significant journal entry, generate exactly one insightful follow-up question or brief observation. Output only that single question or observation—no preamble, no numbering.`

func runProactiveInsight(ctx context.Context, app *infra.App, entryUUID, entryContent string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ctx = infra.WithApp(ctx, app)
	cfg := app.Config()
	if cfg == nil {
		return
	}
	userPrompt := "Entry:\n" + utils.WrapAsUserData(utils.SanitizePrompt(utils.TruncateString(entryContent, 2000)))
	summary, err := infra.GenerateContentSimple(ctx, proactiveInsightPrompt+prompts.DataSafety(), userPrompt, cfg, &infra.GenConfig{MaxOutputTokens: 128})
	if err != nil || strings.TrimSpace(summary) == "" {
		if err != nil {
			infra.LoggerFrom(ctx).Debug("proactive insight LLM failed", "entry_uuid", entryUUID, "error", err)
		}
		return
	}
	bgCtx, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	bgCtx = infra.WithApp(bgCtx, app)
	defer cancel2()
	if _, err := memory.UpsertSemanticMemory(bgCtx, strings.TrimSpace(summary), "thought", "selfmodel", 0.9, nil, []string{entryUUID}); err != nil {
		infra.LoggerFrom(ctx).Debug("proactive insight upsert failed", "entry_uuid", entryUUID, "error", err)
		return
	}
	infra.LoggerFrom(ctx).Info("proactive insight stored", "entry_uuid", entryUUID, "preview", utils.TruncateString(summary, 60))
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
	Journal     string
	RoomContext string // Contains the transcript of the colleagues' discussion
}

// SpecialistOutput is a specialist's response.
type SpecialistOutput struct {
	Domain   Domain
	Summary  string
	Facts    []string
	Entities []string
}

var specialistSystemPrompts = map[Domain]string{
	DomainRelationship: prompts.Specialist("relationship"),
	DomainWork:         prompts.Specialist("work"),
	DomainTask:         prompts.Specialist("task"),
	DomainThought:      prompts.Specialist("thought"),
	DomainSelfModel:    prompts.Specialist("selfmodel"),
	DomainEvolution:    prompts.Specialist("evolution"),
}

// RunSpecialistDiscussion runs a single specialist in "Chat" mode for the Colloquium Room.
// Returns the agent's message, a boolean indicating if they are "DONE", and an error.
func RunSpecialistDiscussion(ctx context.Context, domain Domain, journalContext string, roomTranscript string, modelOverride string) (string, bool, error) {
	ctx, span := infra.StartSpan(ctx, "agent.discuss."+string(domain))
	defer span.End()

	app := infra.GetApp(ctx)
	if app == nil {
		return "", false, fmt.Errorf("no app in context")
	}
	baseSysPrompt := specialistSystemPrompts[domain]

	systemPrompt := baseSysPrompt + `

*** FORMATTING OVERRIDE ***
IGNORE any instructions above about outputting JSON, arrays, or specific fields like 'facts:'. Do NOT use JSON arrays.

You are currently in a colloquium room with other specialist AI agents. Analyze the journal for your domain. Read the Room Transcript to see what your colleagues have said.
Briefly state your findings, point out details others missed, or correct colleagues if they misinterpreted something. Write 2-4 PLAIN TEXT conversational sentences ONLY.

If you completely agree with the current room state and have ABSOLUTELY NOTHING new to add or correct, reply with exactly the word: DONE` + prompts.DataSafety()

	userPrompt := fmt.Sprintf("Journal Context:\n%s\n\nRoom Transcript so far:\n%s",
		utils.WrapAsUserData(utils.SanitizePrompt(journalContext)),
		utils.WrapAsUserData(utils.SanitizePrompt(roomTranscript)))

	infra.LoggerFrom(ctx).Info("specialist discussion request", "domain", domain, "journal_context", journalContext, "room_transcript", roomTranscript)

	apiCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	model := app.Config().GeminiModel
	if modelOverride != "" {
		model = app.Config().DreamerModel
	}
	req := &infra.LLMRequest{
		SystemPrompt: systemPrompt,
		Parts:        []*genai.Part{{Text: userPrompt}},
		Model:        model,
		GenConfig:    &infra.GenConfig{MaxOutputTokens: 512},
	}
	infra.GeminiCallsTotal.Inc()
	resp, err := app.Dispatch(apiCtx, req)
	if err != nil {
		return "", false, infra.WrapLLMError(err)
	}

	text := strings.TrimSpace(infra.ExtractTextFromResponse(resp))

	// Clean up the text to check if the LLM just output "DONE"
	checkText := strings.ToUpper(strings.ReplaceAll(text, ".", ""))
	checkText = strings.ReplaceAll(checkText, "\"", "")
	isDone := checkText == "DONE"
	infra.LoggerFrom(ctx).Info("specialist discussion response", "domain", domain, "response", text, "is_done", isDone)

	return text, isDone, nil
}

// EvolutionAuditOutput is the Cognitive Engineer's nightly analysis.
type EvolutionAuditOutput struct {
	Summary           string   `json:"summary"`
	Facts             []string `json:"facts"`
	Entities          []string `json:"entities"`
	EngineerQuestions []string `json:"engineer_questions"`
}

// RunEvolutionAudit runs the Cognitive Engineer on recent queries.
// personaBriefing and activeContextsSummary are optional "Big Picture" context so the Auditor can suggest mission-level improvements, not just tactical fixes.
func RunEvolutionAudit(ctx context.Context, queriesText, journalSummary, toolManifest, personaBriefing, activeContextsSummary string) (*EvolutionAuditOutput, error) {
	ctx, span := infra.StartSpan(ctx, "agent.evolution_audit")
	defer span.End()

	app := infra.GetApp(ctx)
	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}
	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":            {Type: genai.TypeString, Description: "Architectural health check: 1-3 sentences on overall system friction."},
			"facts":              {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}, Description: "Specific tool or knowledge gaps observed."},
			"entities":           {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}, Description: "Proposals for new tools or code changes (Go-style)."},
			"engineer_questions": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}, Description: "Direct, actionable questions to ask the system engineer about building new tools or filling system capability gaps."},
		},
	}
	systemPrompt := fmt.Sprintf(prompts.Specialist("evolution"), toolManifest) + prompts.DataSafety()
	userPrompt := "## SYSTEM CAPABILITIES (Existing Tools):\n" + utils.WrapAsUserData(toolManifest) + "\n\n"
	if personaBriefing != "" {
		userPrompt += "## USER PERSONA (who you are serving):\n" + utils.WrapAsUserData(utils.SanitizePrompt(personaBriefing)) + "\n\n"
	}
	if activeContextsSummary != "" {
		userPrompt += "## ACTIVE CONTEXTS (what the system treats as important right now):\n" + utils.WrapAsUserData(utils.SanitizePrompt(activeContextsSummary)) + "\n\n"
	}
	userPrompt += "Given the user's stated values and current active contexts above, identify gaps between what the system does and what would make the user more effective. Do not only fix local bugs—consider agency, momentum, and alignment with the user's goals.\n\n"
	userPrompt += "Analyze the following user-assistant interaction log for Process Friction. Identify tool efficacy issues, knowledge gaps, and propose concrete improvements (new tools or Go code changes).\n\nRECENT QUERIES AND ANSWERS:\n" + utils.WrapAsUserData(utils.SanitizePrompt(queriesText))
	if journalSummary != "" {
		userPrompt += "\n\nRECENT JOURNAL THEMES (for context):\n" + utils.WrapAsUserData(utils.SanitizePrompt(journalSummary))
	}
	req := &infra.LLMRequest{
		SystemPrompt:   systemPrompt,
		Parts:         []*genai.Part{{Text: userPrompt}},
		Model:         app.Config().DreamerModel,
		GenConfig:     &infra.GenConfig{MaxOutputTokens: 2048, TopP: 0.9, ResponseMIMEType: infra.MIMETypeJSON},
		ResponseSchema: schema,
	}
	apiCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	infra.GeminiCallsTotal.Inc()
	resp, err := app.Dispatch(apiCtx, req)
	if err != nil {
		span.RecordError(err)
		return nil, infra.WrapLLMError(err)
	}

	text := infra.ExtractTextFromResponse(resp)
	out, parseErr := llmjson.ParseLLMResponse[EvolutionAuditOutput](text, []string{"summary", "facts", "entities", "engineer_questions"})
	if out == nil {
		// Best-effort: try repair-only parse so evolution synthesis can still write something.
		var repaired EvolutionAuditOutput
		if err := llmjson.RepairAndUnmarshal(text, &repaired); err == nil {
			infra.LoggerFrom(ctx).Info("evolution_audit recovered via repair", "summary_len", len(repaired.Summary), "facts", len(repaired.Facts))
			return &repaired, nil
		}
		if parseErr == nil {
			parseErr = errors.New("parse failed")
		}
		infra.LoggerFrom(ctx).Warn("evolution_audit parse failed, returning minimal output", "error", parseErr, "raw", utils.TruncateString(text, 400))
		// Return minimal valid output so RunEvolutionSynthesis can still update system_evolution.
		return &EvolutionAuditOutput{
			Summary:           "Evolution audit response was truncated or malformed; partial raw: " + utils.TruncateString(strings.TrimSpace(text), 500),
			Facts:             nil,
			Entities:          nil,
			EngineerQuestions: nil,
		}, nil
	}
	infra.LoggerFrom(ctx).Info("evolution_audit done", "summary_len", len(out.Summary), "facts", len(out.Facts), "entities", len(out.Entities), "engineer_questions", len(out.EngineerQuestions))
	return out, nil
}

// RunSpecialist runs a single specialist agent for final JSON extraction.
func RunSpecialist(ctx context.Context, domain Domain, input *SpecialistInput, modelOverride string) (*SpecialistOutput, error) {
	ctx, span := infra.StartSpan(ctx, "agent."+string(domain))
	defer span.End()

	t0 := time.Now()
	app := infra.GetApp(ctx)
	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}
	clientMs := time.Since(t0).Milliseconds()
	infra.LoggerFrom(ctx).Info("specialist client_ready", "domain", domain, "client_ms", clientMs)

	prompt := fmt.Sprintf(`User message:
%s

%s`, utils.WrapAsUserData(utils.SanitizePrompt(input.UserMessage)), utils.WrapAsUserData(utils.SanitizePrompt(input.Context)))

	if input.Journal != "" {
		prompt = fmt.Sprintf(`User message:
%s

Recent journal context:
%s

%s`, utils.WrapAsUserData(utils.SanitizePrompt(input.UserMessage)), utils.WrapAsUserData(utils.SanitizePrompt(input.Journal)), utils.WrapAsUserData(utils.SanitizePrompt(input.Context)))
	}

	// Inject the room discussion if it exists
	if input.RoomContext != "" {
		prompt += fmt.Sprintf("\n\nColloquium Room Discussion (Your colleagues' notes):\n%s\n\nReview this discussion. Use it to resolve conflicts, fix assumptions, and refine your final extraction. Do not extract facts that your colleagues have proven wrong.", utils.WrapAsUserData(utils.SanitizePrompt(input.RoomContext)))
	}

	infra.LoggerFrom(ctx).Info("specialist prompt", "domain", domain, "prompt_len", len(prompt), "prompt", prompt)

	model := app.Config().GeminiModel
	if modelOverride != "" {
		model = app.Config().DreamerModel
	}
	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary":  {Type: genai.TypeString},
			"facts":    {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"entities": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		},
	}
	systemPrompt := specialistSystemPrompts[domain] +
		"\n\nCRITICAL: Output at most 5 facts. Use the format '[CATEGORY] Subject: Detail'. " +
		"Keep summaries to 1-2 sentences. Avoid conversational filler." +
		prompts.DataSafety()
	req := &infra.LLMRequest{
		SystemPrompt:   systemPrompt,
		Parts:          []*genai.Part{{Text: prompt}},
		Model:          model,
		GenConfig:      &infra.GenConfig{MaxOutputTokens: 2048, TopP: 0.9, ResponseMIMEType: infra.MIMETypeJSON},
		ResponseSchema: schema,
	}

	apiCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	infra.GeminiCallsTotal.Inc()
	infra.LoggerFrom(ctx).Info("specialist api_call_start", "domain", domain, "model", model)
	t1 := time.Now()
	resp, err := app.Dispatch(apiCtx, req)
	apiMs := time.Since(t1).Milliseconds()

	if err != nil && (errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "context deadline exceeded")) {
		infra.LoggerFrom(ctx).Warn("specialist api timeout, retrying", "domain", domain, "api_ms", apiMs)
		apiCtx2, cancel2 := context.WithTimeout(ctx, 60*time.Second)
		defer cancel2()
		t1 = time.Now()
		resp, err = app.Dispatch(apiCtx2, req)
		apiMs = time.Since(t1).Milliseconds()
	}

	if err != nil {
		span.RecordError(err)
		infra.LoggerFrom(ctx).Warn("specialist api failed", "domain", domain, "api_ms", apiMs, "error", err)
		return nil, infra.WrapLLMError(err)
	}
	infra.LoggerFrom(ctx).Info("specialist api_done", "domain", domain, "api_ms", apiMs)

	text := infra.ExtractTextFromResponse(resp)
	totalMs := time.Since(t0).Milliseconds()
	infra.LoggerFrom(ctx).Info("specialist total", "domain", domain, "total_ms", totalMs, "client_ms", clientMs, "api_ms", apiMs)
	infra.LoggerFrom(ctx).Info("specialist response", "domain", domain, "response_len", len(text), "response", text)

	var parsed struct {
		Summary  string   `json:"summary"`
		Facts    []string `json:"facts"`
		Entities []string `json:"entities"`
	}
	parsedOut, parseErr := llmjson.ParseLLMResponse[struct {
		Summary  string   `json:"summary"`
		Facts    []string `json:"facts"`
		Entities []string `json:"entities"`
	}](text, []string{"summary", "facts", "entities"})
	if parsedOut == nil {
		infra.LoggerFrom(ctx).Warn("specialist output parse failed", "domain", domain, "error", parseErr, "raw_response_len", len(text), "raw", text)
		return &SpecialistOutput{Domain: domain, Summary: strings.TrimSpace(text)}, nil
	}
	parsed = *parsedOut

	if len(parsed.Facts) == 0 {
		infra.LoggerFrom(ctx).Info("specialist returned 0 facts", "domain", domain, "summary", parsed.Summary, "raw_response_len", len(text), "raw_response", text)
	}

	return &SpecialistOutput{
		Domain:   domain,
		Summary:  parsed.Summary,
		Facts:    parsed.Facts,
		Entities: parsed.Entities,
	}, nil
}

const contextExtractorPrompt = `From the journal entries, list ongoing projects, plans, or events as short snake_case names only (e.g. party_planning, job_search, vacation_research). Ignore one-off questions and system commands. Return JSON: {"impacted_contexts": ["name1", "name2"]}.`

// RunContextExtractor uses Gemini to extract impacted_contexts from the journal.
func RunContextExtractor(ctx context.Context, journalContext string) ([]string, error) {
	ctx, span := infra.StartSpan(ctx, "agent.context_extractor")
	defer span.End()

	app := infra.GetApp(ctx)
	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}
	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"impacted_contexts": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
		},
	}
	userPrompt := "Journal entries:\n" + utils.WrapAsUserData(utils.SanitizePrompt(journalContext))
	req := &infra.LLMRequest{
		SystemPrompt:   contextExtractorPrompt + prompts.DataSafety(),
		Parts:         []*genai.Part{{Text: userPrompt}},
		Model:         app.Config().DreamerModel,
		GenConfig:     &infra.GenConfig{MaxOutputTokens: 1024, TopP: 0.9, ResponseMIMEType: infra.MIMETypeJSON},
		ResponseSchema: schema,
	}
	apiCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	infra.GeminiCallsTotal.Inc()
	infra.LoggerFrom(ctx).Info("context_extractor api_call_start", "journal_len", len(journalContext))
	resp, err := app.Dispatch(apiCtx, req)
	if err != nil {
		span.RecordError(err)
		return nil, infra.WrapLLMError(err)
	}

	text := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	if text == "" {
		infra.LoggerFrom(ctx).Debug("context_extractor empty response")
		return nil, nil
	}
	parsedOut, parseErr := llmjson.ParseLLMResponse[struct{ ImpactedContexts []string }](text, []string{"impacted_contexts"})
	if parseErr != nil || parsedOut == nil {
		infra.LoggerFrom(ctx).Warn("context_extractor parse failed", "error", parseErr, "raw", utils.TruncateString(text, 300))
		// Return empty slice (not nil) so dreamer continues with no impacted contexts instead of nil.
		return []string{}, nil
	}
	infra.LoggerFrom(ctx).Info("context_extractor done", "count", len(parsedOut.ImpactedContexts))
	return parsedOut.ImpactedContexts, nil
}

const queryAnalyzerPrompt = `Analyze the user's recent queries. (1) Group them into semantic clusters (e.g. "Jot Development", "Family Logistics"). (2) Identify Knowledge Gaps: What is the user asking that we couldn't answer? (3) Identify Curiosity Trends: What is the user becoming more interested in? Return JSON with a single string field "query_analysis" containing the full analysis.`

// RunQueryAnalyzer uses Gemini to analyze recent queries.
func RunQueryAnalyzer(ctx context.Context, recentQueriesText string) (string, error) {
	if strings.TrimSpace(recentQueriesText) == "" {
		return "", nil
	}

	ctx, span := infra.StartSpan(ctx, "agent.query_analyzer")
	defer span.End()

	app := infra.GetApp(ctx)
	if app == nil {
		return "", fmt.Errorf("no app in context")
	}
	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"query_analysis": {Type: genai.TypeString, Description: "Analysis: semantic clusters, knowledge gaps, curiosity trends"},
		},
	}
	userPrompt := "Recent queries:\n" + utils.WrapAsUserData(utils.SanitizePrompt(recentQueriesText))
	req := &infra.LLMRequest{
		SystemPrompt:   queryAnalyzerPrompt + prompts.DataSafety(),
		Parts:         []*genai.Part{{Text: userPrompt}},
		Model:         app.Config().DreamerModel,
		GenConfig:     &infra.GenConfig{MaxOutputTokens: 1024, TopP: 0.9, ResponseMIMEType: infra.MIMETypeJSON},
		ResponseSchema: schema,
	}
	apiCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	infra.GeminiCallsTotal.Inc()
	infra.LoggerFrom(ctx).Info("query_analyzer api_call_start", "queries_len", len(recentQueriesText))
	resp, err := app.Dispatch(apiCtx, req)
	if err != nil {
		span.RecordError(err)
		return "", infra.WrapLLMError(err)
	}

	text := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	if text == "" {
		infra.LoggerFrom(ctx).Debug("query_analyzer empty response")
		return "", nil
	}
	parsedOut, parseErr := llmjson.ParseLLMResponse[struct{ QueryAnalysis string }](text, []string{"query_analysis"})
	if parseErr != nil || parsedOut == nil {
		infra.LoggerFrom(ctx).Warn("query_analyzer parse failed", "error", parseErr, "raw", utils.TruncateString(text, 300))
		return "", nil
	}
	out := strings.TrimSpace(parsedOut.QueryAnalysis)
	infra.LoggerFrom(ctx).Info("query_analyzer done", "len", len(out))
	return out, nil
}

// DecompositionResult is the output of the decomposition step.
type DecompositionResult struct {
	Domains []Domain `json:"domains"`
}

// DecomposeMessage uses the LLM to determine which specialists to consult.
func DecomposeMessage(ctx context.Context, userMessage string) ([]Domain, error) {
	ctx, span := infra.StartSpan(ctx, "dispatcher.decompose")
	defer span.End()

	app := infra.GetApp(ctx)
	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}
	schema := &genai.Schema{
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
	prompt := fmt.Sprintf("User message:\n%s\n\nWhich domains to consult?", utils.WrapAsUserData(utils.SanitizePrompt(userMessage)))
	req := &infra.LLMRequest{
		SystemPrompt:   systemPrompt,
		Parts:         []*genai.Part{{Text: prompt}},
		Model:         app.Config().GeminiModel,
		GenConfig:     &infra.GenConfig{MaxOutputTokens: 1024, ResponseMIMEType: infra.MIMETypeJSON},
		ResponseSchema: schema,
	}
	infra.GeminiCallsTotal.Inc()
	resp, err := app.Dispatch(ctx, req)
	if err != nil {
		span.RecordError(err)
		return nil, infra.WrapLLMError(err)
	}

	text := infra.ExtractTextFromResponse(resp)
	var result DecompositionResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		infra.LoggerFrom(ctx).Warn("decomposition parse failed", "error", err, "raw", utils.TruncateString(text, 200))
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

// RunCommittee runs the selected specialists in parallel.
func RunCommittee(ctx context.Context, userMessage, journalContext string, domains []Domain) ([]*SpecialistOutput, error) {
	ctx, span := infra.StartSpan(ctx, "dispatcher.committee")
	defer span.End()

	input := &SpecialistInput{
		UserMessage: userMessage,
		Context:     "Extract relevant facts and provide a brief summary.",
		Journal:     journalContext,
	}

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
		return nil, infra.WrapLLMError(err)
	}

	return outputs, nil
}
