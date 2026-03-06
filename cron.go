package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/llmjson"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/api/iterator"
)

const (
	JanitorWeightThreshold   = 0.2
	JanitorStaleDays         = 30
	PulseStaleDays           = 14
	PulseImportanceThreshold = 0.7
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// PulseResult holds the outcome of a pulse audit run.
type PulseResult struct {
	StaleNodes []string
	Signals    int
}

// gapDetectItem is one item from the gap-detection LLM response (array of objects).
type gapDetectItem struct {
	Kind     string `json:"kind"`
	Question string `json:"question"`
	Context  string `json:"context"`
}

// RunGapDetection compares the last 24h journal to relevant knowledge and appends any gaps/contradictions to PendingQuestions.
func RunGapDetection(ctx context.Context, journalContext string, entryUUIDs []string) error {
	ctx, span := StartSpan(ctx, "cron.gap_detection")
	defer span.End()

	if len(journalContext) < 100 {
		return nil
	}
	vec, err := GenerateEmbedding(ctx, journalContext, EmbedTaskRetrievalDocument)
	if err != nil {
		return fmt.Errorf("gap detection embedding: %w", err)
	}
	nodes, err := QuerySimilarNodes(ctx, vec, 15)
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

	client, err := GetGeminiClient(ctx)
	if err != nil {
		return err
	}
	model := client.GenerativeModel(GetEffectiveModel(ctx, defaultConfig.DreamerModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(1024)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeArray,
		Items: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"kind":     {Type: genai.TypeString},
				"question": {Type: genai.TypeString},
				"context":  {Type: genai.TypeString},
			},
		},
	}
	userPrompt := prompts.FormatGapDetector(WrapAsUserData(SanitizePrompt(journalContext)), WrapAsUserData(relevantKnowledge))

	apiCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	GeminiCallsTotal.Inc()
	resp, err := model.GenerateContent(apiCtx, genai.Text(userPrompt))
	if err != nil {
		span.RecordError(err)
		return WrapLLMError(err)
	}
	text := ExtractText(resp)
	var items []gapDetectItem
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		if err := llmjson.RepairAndUnmarshal(text, &items); err != nil {
			LoggerFrom(ctx).Debug("gap detection parse failed", "error", err, "raw", utils.TruncateString(text, 300))
			return nil
		}
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
	return memory.InsertPendingQuestions(ctx, questions)
}

// RunDreamer consolidates the last 24h of journal entries into semantic memory.
func RunDreamer(ctx context.Context) (*DreamerResult, error) {
	return agent.RunDreamer(ctx, jotFOHEnv{})
}

// RunProfileSynthesis merges new persona facts into the permanent user_profile context node.
func RunProfileSynthesis(ctx context.Context, personaFacts []string) error {
	ctx, span := StartSpan(ctx, "cron.profile_synthesis")
	defer span.End()

	if len(personaFacts) == 0 {
		return nil
	}

	node, _, err := memory.FindContextByName(ctx, "user_profile")
	if err != nil || node == nil {
		return fmt.Errorf("user_profile node not found: %w", err)
	}

	userPrompt := fmt.Sprintf("Current Profile:\n%s\n\nNew Identity Markers:\n%s",
		WrapAsUserData(node.Content), WrapAsUserData(strings.Join(personaFacts, "\n")))

	newProfile, err := GenerateContentSimple(ctx, prompts.IdentityArchitect(), userPrompt, &GenConfig{MaxOutputTokens: 1024, ModelOverride: defaultConfig.DreamerModel})
	if err != nil {
		return err
	}

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}

	_, err = client.Collection(KnowledgeCollection).Doc(node.UUID).Update(ctx, []firestore.Update{
		{Path: "content", Value: strings.TrimSpace(newProfile)},
		{Path: "timestamp", Value: time.Now().Format(time.RFC3339)},
	})
	if err != nil {
		return err
	}

	LoggerFrom(ctx).Info("profile synthesis completed", "uuid", node.UUID)
	return nil
}

// RunEvolutionSynthesis runs the Cognitive Engineer on recent queries (and journal summary), then writes the result to the system_evolution context.
func RunEvolutionSynthesis(ctx context.Context, journalSummary string) error {
	ctx, span := StartSpan(ctx, "cron.evolution_synthesis")
	defer span.End()

	queries, err := GetRecentQueries(ctx, 50)
	if err != nil {
		return fmt.Errorf("get recent queries: %w", err)
	}
	queriesText := FormatQueriesForContext(queries, 8000)
	if queriesText == "" || strings.TrimSpace(queriesText) == "No queries found." {
		LoggerFrom(ctx).Info("evolution_synthesis: no queries to audit")
		return nil
	}

	// Optional: shorten journal summary for evolution context only
	journalForEvolution := ""
	if len(journalSummary) > 2000 {
		journalForEvolution = utils.TruncateToMaxBytes(journalSummary, 2000) + "\n... (truncated)"
	} else {
		journalForEvolution = journalSummary
	}

	audit, err := RunEvolutionAudit(ctx, queriesText, journalForEvolution)
	if err != nil {
		return err
	}

	node, _, err := memory.FindContextByName(ctx, "system_evolution")
	if err != nil || node == nil {
		return fmt.Errorf("system_evolution context not found: %w", err)
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

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(KnowledgeCollection).Doc(node.UUID).Update(ctx, []firestore.Update{
		{Path: "content", Value: content},
		{Path: "timestamp", Value: time.Now().Format(time.RFC3339)},
	})
	if err != nil {
		return err
	}
	LoggerFrom(ctx).Info("evolution synthesis wrote system_evolution", "uuid", node.UUID)
	return nil
}

func stringListWithBullets(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = "- " + v
	}
	return out
}

// RunJanitor performs garbage collection on semantic memory.
func RunJanitor(ctx context.Context) (int, error) {
	ctx, span := StartSpan(ctx, "cron.janitor")
	defer span.End()

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}

	cutoff := time.Now().AddDate(0, 0, -JanitorStaleDays)
	cutoffStr := cutoff.Format(time.RFC3339)

	// Query nodes with low weight and stale recall
	// Note: Requires composite index on (significance_weight, last_recalled_at)
	iter := client.Collection(KnowledgeCollection).
		Where("significance_weight", "<", JanitorWeightThreshold).
		Where("last_recalled_at", "<", cutoffStr).
		Documents(ctx)
	defer iter.Stop()

	deleted := 0
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			span.RecordError(err)
			return deleted, WrapFirestoreIndexError(err)
		}

		data := doc.Data()
		projectID := GetLinkedCompletedProjectID(ctx, data)
		if projectID != "" {
			content := getStringField(data, "content")
			if content != "" {
				if err := AppendToProjectArchiveSummary(ctx, projectID, content); err != nil {
					LoggerFrom(ctx).Warn("janitor archive append failed", "project_id", projectID, "error", err)
				} else {
					LoggerFrom(ctx).Debug("janitor squeezed into project", "id", doc.Ref.ID, "project_id", projectID)
				}
			}
		}

		if _, err := doc.Ref.Delete(ctx); err != nil {
			LoggerFrom(ctx).Warn("janitor delete failed", "id", doc.Ref.ID, "error", err)
			continue
		}
		deleted++
		LoggerFrom(ctx).Debug("janitor evicted", "id", doc.Ref.ID)
	}

	LoggerFrom(ctx).Info("janitor completed", "deleted", deleted)
	span.SetAttributes(map[string]string{"deleted": fmt.Sprintf("%d", deleted)})
	return deleted, nil
}

// RunPulseAudit identifies high-value nodes (project, goal, person) that have not been recalled in PulseStaleDays and creates a proactive signal for each.
func RunPulseAudit(ctx context.Context) (*PulseResult, error) {
	ctx, span := StartSpan(ctx, "cron.pulse_audit")
	defer span.End()

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	staleThreshold := time.Now().AddDate(0, 0, -PulseStaleDays).Format(time.RFC3339)

	// Requires composite index: node_type, significance_weight, last_recalled_at
	iter := client.Collection(KnowledgeCollection).
		Where("node_type", "in", []string{"project", "goal", "person"}).
		Where("significance_weight", ">=", PulseImportanceThreshold).
		Where("last_recalled_at", "<", staleThreshold).
		Documents(ctx)
	defer iter.Stop()

	result := &PulseResult{}
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			span.RecordError(err)
			return result, WrapFirestoreIndexError(err)
		}

		data := doc.Data()
		nodeID := doc.Ref.ID
		content := getStringField(data, "content")

		signalContent := fmt.Sprintf("STALE LOOP DETECTED: You haven't mentioned '%s' in 2 weeks. Is this still a priority?", content)
		_, err = UpsertSemanticMemory(ctx, signalContent, "thought", "selfmodel", 0.9, []string{nodeID}, nil)
		if err != nil {
			LoggerFrom(ctx).Warn("failed to create pulse signal", "node_id", nodeID, "error", err)
			continue
		}

		result.StaleNodes = append(result.StaleNodes, nodeID)
		result.Signals++
		LoggerFrom(ctx).Info("pulse audit flagged node", "id", nodeID, "content", utils.TruncateString(content, 40))
	}

	span.SetAttributes(map[string]string{
		"stale_nodes": fmt.Sprintf("%d", len(result.StaleNodes)),
		"signals":     fmt.Sprintf("%d", result.Signals),
	})
	return result, nil
}
