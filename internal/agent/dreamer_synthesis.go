package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
)

type gapDetectItem struct {
	Kind     string `json:"kind"`
	Question string `json:"question"`
	Context  string `json:"context"`
}

// RunGapDetection compares the last 24h journal to relevant knowledge and appends any gaps/contradictions to PendingQuestions.
func RunGapDetection(ctx context.Context, app *infra.App, journalContext string, entryUUIDs []string) error {
	ctx, span := infra.StartSpan(ctx, "cron.gap_detection")
	defer span.End()

	if len(journalContext) < 100 {
		return nil
	}
	if app == nil || app.Config() == nil {
		return fmt.Errorf("app config required for gap detection")
	}
	vec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, journalContext, infra.EmbedTaskRetrievalDocument)
	if err != nil {
		return fmt.Errorf("gap detection embedding: %w", err)
	}
	nodes, err := memory.QuerySimilarNodes(ctx, app, vec, 15)
	if err != nil {
		return fmt.Errorf("gap detection query nodes: %w", err)
	}
	var knowledgeLines []string
	for _, n := range nodes {
		knowledgeLines = append(knowledgeLines, fmt.Sprintf("[%s] %s", n.NodeType, utils.TruncateString(n.Content, 200)))
	}
	relevantKnowledge := strings.Join(knowledgeLines, "\n")
	if len(relevantKnowledge) > 4000 {
		relevantKnowledge = utils.TruncateToMaxBytes(relevantKnowledge, 4000) + "\n... (truncated)"
	}

	// Inject app capabilities so the gap-detector LLM knows what Jot can do (entry points, agents, memory, tools).
	capabilitiesAndTools := prompts.AppCapabilities() + "\n\n## Existing tools (compact)\n" + tools.GetCompactDirectory()
	userPrompt, err := prompts.BuildGapDetector(prompts.GapDetectorData{
		RecentJournal:      utils.WrapAsUserData(utils.SanitizePrompt(journalContext)),
		RelevantKnowledge:  utils.WrapAsUserData(relevantKnowledge),
		ToolManifest:        utils.WrapAsUserData(capabilitiesAndTools),
	})
	if err != nil {
		return fmt.Errorf("build gap detector prompt: %w", err)
	}
	req := &infra.LLMRequest{
		Parts:     []*genai.Part{{Text: userPrompt}},
		Model:     app.Config().DreamerModel,
		GenConfig: &infra.GenConfig{MaxOutputTokens: 1024},
	}
	apiCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	infra.GeminiCallsTotal.Inc()
	resp, err := app.Dispatch(apiCtx, req)
	if err != nil {
		span.RecordError(err)
		return infra.WrapLLMError(err)
	}
	text := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	_, sections := utils.ParseKeyValueMap(text)
	issueLines := sections["issues"]
	var items []gapDetectItem
	for _, line := range issueLines {
		parts := strings.SplitN(line, " | ", 3)
		if len(parts) < 2 {
			continue
		}
		kind := strings.TrimSpace(strings.ToLower(parts[0]))
		question := strings.TrimSpace(parts[1])
		contextStr := ""
		if len(parts) >= 3 {
			contextStr = strings.TrimSpace(parts[2])
		}
		items = append(items, gapDetectItem{Kind: kind, Question: question, Context: contextStr})
	}
	if len(items) == 0 {
		return nil
	}
	questions := make([]memory.PendingQuestion, 0, len(items))
	for _, it := range items {
		kind := strings.TrimSpace(strings.ToLower(it.Kind))
		if kind != "gap" && kind != "contradiction" {
			kind = "gap"
		}
		questions = append(questions, memory.PendingQuestion{
			Question:       strings.TrimSpace(it.Question),
			Kind:           kind,
			Context:        strings.TrimSpace(it.Context),
			SourceEntryIDs: entryUUIDs,
		})
	}
	return memory.InsertPendingQuestions(ctx, app, questions)
}

// RunProfileSynthesis merges new persona facts into the permanent user_profile context node.
func RunProfileSynthesis(ctx context.Context, app *infra.App, personaFacts []string) error {
	ctx, span := infra.StartSpan(ctx, "cron.profile_synthesis")
	defer span.End()

	if len(personaFacts) == 0 {
		return nil
	}

	node, _, err := memory.FindContextByName(ctx, app, "user_profile")
	if err != nil || node == nil {
		return fmt.Errorf("user_profile node not found: %w", err)
	}

	userPrompt := fmt.Sprintf("Current Profile:\n%s\n\nNew Identity Markers:\n%s",
		utils.WrapAsUserData(node.Content), utils.WrapAsUserData(strings.Join(personaFacts, "\n")))

	if app == nil || app.Config() == nil {
		return fmt.Errorf("app config required for profile synthesis")
	}
	newProfile, err := infra.GenerateContentSimple(ctx, app, prompts.IdentityArchitect(), userPrompt, app.Config(), &infra.GenConfig{MaxOutputTokens: 1024, ModelOverride: app.Config().DreamerModel})
	if err != nil {
		return err
	}

	client, err := app.Firestore(ctx)
	if err != nil {
		return err
	}

	_, err = client.Collection(memory.KnowledgeCollection).Doc(node.UUID).Update(ctx, []firestore.Update{
		{Path: "content", Value: strings.TrimSpace(newProfile)},
		{Path: "timestamp", Value: time.Now().Format(time.RFC3339)},
	})
	if err != nil {
		return err
	}

	infra.LoggerFrom(ctx).Info("profile synthesis completed", "uuid", node.UUID)
	return nil
}

// RunEvolutionSynthesis runs the Cognitive Engineer on recent queries (and journal summary), writes the result to the system_evolution context, and returns the audit for use by the Dream Narrative.
func RunEvolutionSynthesis(ctx context.Context, env infra.ToolEnv, journalSummary string) (*EvolutionAuditOutput, error) {
	ctx, span := infra.StartSpan(ctx, "cron.evolution_synthesis")
	defer span.End()

	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	queries, err := journal.GetRecentQueries(ctx, client, 50)
	if err != nil {
		return nil, fmt.Errorf("get recent queries: %w", err)
	}
	queriesText := journal.FormatQueriesForContext(queries, 8000)
	if queriesText == "" || strings.TrimSpace(queriesText) == "No queries found." {
		infra.LoggerFrom(ctx).Info("evolution_synthesis: no queries to audit")
		return nil, nil
	}

	journalForEvolution := ""
	if len(journalSummary) > 2000 {
		journalForEvolution = utils.TruncateToMaxBytes(journalSummary, 2000) + "\n... (truncated)"
	} else {
		journalForEvolution = journalSummary
	}

	// Big Picture: pass user persona and active contexts so the Auditor can reason about mission-level gaps.
	personaContent := ""
	if node, _, err := memory.FindContextByName(ctx, env, "user_profile"); err == nil && node != nil && node.Content != "" {
		personaContent = node.Content
	}
	activeContextsSummary := ""
	if nodes, metas, err := memory.GetActiveContexts(ctx, env, 10); err == nil && len(nodes) > 0 {
		var lines []string
		for i := range nodes {
			if i >= len(metas) {
				break
			}
			name := metas[i].ContextName
			content := nodes[i].Content
			first := utils.FirstSentence(content, 120)
			lines = append(lines, fmt.Sprintf("%s: %s", name, first))
		}
		activeContextsSummary = strings.Join(lines, "\n")
	}

	toolManifest := tools.GetCompactDirectory()
	audit, err := RunEvolutionAudit(ctx, env, queriesText, journalForEvolution, toolManifest, personaContent, activeContextsSummary)
	if err != nil {
		return nil, err
	}

	// EngineerQuestions are no longer written to PendingQuestions; they are passed to the Dream Narrative only.

	node, _, err := memory.FindContextByName(ctx, env, "system_evolution")
	if err != nil || node == nil {
		return nil, fmt.Errorf("system_evolution context not found: %w", err)
	}

	dateStr := time.Now().Format("January 2, 2006")
	var sections []string
	sections = append(sections, fmt.Sprintf("System Evolution Audit (%s):\n\n%s", dateStr, audit.Summary))
	if len(audit.Facts) > 0 {
		sections = append(sections, "Friction / knowledge gaps:\n"+strings.Join(stringListWithBullets(audit.Facts), "\n"))
	}
	if len(audit.Entities) > 0 {
		sections = append(sections, "Recommended Go/tool changes:\n"+strings.Join(stringListWithBullets(audit.Entities), "\n"))
	}
	content := strings.Join(sections, "\n\n")

	if client == nil {
		return nil, fmt.Errorf("firestore client required to write system_evolution")
	}
	_, err = client.Collection(memory.KnowledgeCollection).Doc(node.UUID).Update(ctx, []firestore.Update{
		{Path: "content", Value: content},
		{Path: "timestamp", Value: time.Now().Format(time.RFC3339)},
	})
	if err != nil {
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("evolution synthesis wrote system_evolution", "uuid", node.UUID)
	return audit, nil
}

func stringListWithBullets(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = "- " + v
	}
	return out
}
