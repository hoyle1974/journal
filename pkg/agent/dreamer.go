package agent

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"golang.org/x/sync/errgroup"
)

const (
	DreamerMergeSimilarity         = 0.93 // cosine similarity above this = same fact
	DreamerBaseWeight              = 0.7
	DreamerWeightBoostPerDup      = 0.1
	DreamerSynthesisNewLogsThreshold = 3  // run synthesis if this many new entries since last
	DreamerSynthesisStaleHours    = 48   // high-significance contexts re-synthesize if older than this
)

// DreamerInputs holds loaded data for a dream run.
type DreamerInputs struct {
	JournalContext    string
	EntryUUIDs        []string
	RecentQueriesText string
}

// DreamerResult holds the outcome of a dream run for diagnostics.
type DreamerResult struct {
	EntriesProcessed    int
	FactsExtracted      int
	FactsWritten        int
	ContextsSynthesized int
}

type mergedFact struct {
	Content  string
	NodeType string
	Domain   string
	Weight   float64
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func mergeDreamerFacts(ctx context.Context, domains []Domain, outputs []*SpecialistOutput) []mergedFact {
	type factWithMeta struct {
		fact   string
		domain Domain
		vec    []float32
	}
	var flat []factWithMeta
	for i, domain := range domains {
		out := outputs[i]
		if out == nil {
			continue
		}
		for _, f := range out.Facts {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			vec, err := generateEmbedding(ctx, f)
			if err != nil {
				infra.LoggerFrom(ctx).Debug("dreamer merge skip embedding", "fact", utils.TruncateString(f, 40), "error", err)
				continue
			}
			flat = append(flat, factWithMeta{fact: f, domain: domain, vec: vec})
		}
	}
	if len(flat) == 0 {
		return nil
	}
	var groups [][]int
	used := make([]bool, len(flat))
	for i := range flat {
		if used[i] {
			continue
		}
		group := []int{i}
		used[i] = true
		for j := i + 1; j < len(flat); j++ {
			if used[j] {
				continue
			}
			sim := cosineSimilarity(flat[i].vec, flat[j].vec)
			if sim >= DreamerMergeSimilarity {
				group = append(group, j)
				used[j] = true
			}
		}
		groups = append(groups, group)
	}
	var result []mergedFact
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		bestIdx := g[0]
		for _, idx := range g[1:] {
			if len(flat[idx].fact) > len(flat[bestIdx].fact) {
				bestIdx = idx
			}
		}
		f := flat[bestIdx]
		nodeType := "fact"
		if f.domain == DomainRelationship {
			nodeType = "person"
		} else if f.domain == DomainWork {
			nodeType = "project"
		}
		weight := DreamerBaseWeight + DreamerWeightBoostPerDup*float64(len(g)-1)
		if weight > 1 {
			weight = 1
		}
		result = append(result, mergedFact{
			Content:  f.fact,
			NodeType: nodeType,
			Domain:   string(f.domain),
			Weight:   weight,
		})
	}
	return result
}

func shouldSynthesizeContext(meta *memory.ContextMetadata) bool {
	const pulseImportanceThreshold = 0.7
	neverSynthesized := meta.LastSynthesizedAt == "" && meta.SourceEntryCountAtSynthesis == 0
	if neverSynthesized {
		return true
	}
	newLogs := len(meta.SourceEntries) > meta.SourceEntryCountAtSynthesis+DreamerSynthesisNewLogsThreshold
	if newLogs {
		return true
	}
	if meta.Relevance < pulseImportanceThreshold {
		return false
	}
	if meta.LastSynthesizedAt == "" {
		return true
	}
	lastSynth, err := time.Parse(time.RFC3339, meta.LastSynthesizedAt)
	if err != nil {
		return true
	}
	return time.Since(lastSynth) > DreamerSynthesisStaleHours*time.Hour
}

func dreamerWriteMergedFacts(ctx context.Context, merged []mergedFact, entryUUIDs []string) (written int, err error) {
	for _, m := range merged {
		if _, err = memory.UpsertSemanticMemory(ctx, m.Content, m.NodeType, m.Domain, m.Weight, nil, entryUUIDs); err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer upsert failed", "domain", m.Domain, "fact", utils.TruncateString(m.Content, 50), "error", err)
			continue
		}
		written++
		infra.LoggerFrom(ctx).Info("dreamer wrote fact", "domain", m.Domain, "fact", utils.TruncateString(m.Content, 60), "n", written, "total", len(merged))
	}
	return written, nil
}

func dreamerSynthesizeContexts(ctx context.Context, contextUUIDs map[string]struct{}) (synthesized, skippedLazy int, err error) {
	for uuid := range contextUUIDs {
		meta, err := memory.GetContextMetadata(ctx, uuid)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer get context metadata failed", "context_uuid", uuid, "error", err)
			continue
		}
		if !shouldSynthesizeContext(meta) {
			skippedLazy++
			if err = memory.TouchContext(ctx, uuid, nil, 0); err != nil {
				infra.LoggerFrom(ctx).Debug("dreamer touch context skipped", "context_uuid", uuid, "error", err)
			}
			continue
		}
		if err = memory.SynthesizeContext(ctx, uuid); err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer context synthesis failed", "context_uuid", uuid, "error", err)
			continue
		}
		synthesized++
	}
	return synthesized, skippedLazy, nil
}

const systemCollection = "_system"
const latestDreamDoc = "latest_dream"

// dreamNarrativeInput holds the data passed to the Dream Narrative generator.
type dreamNarrativeInput struct {
	EntriesProcessed    int
	FactsExtracted      int
	FactsWritten        int
	ContextsSynthesized int
	PersonaFacts        []string
	EvolutionAudit      *EvolutionAuditOutput
}

// writeDreamNarrative generates a morning readout from the dream run and saves it to Firestore _system/latest_dream.
func writeDreamNarrative(ctx context.Context, app *infra.App, in *dreamNarrativeInput) error {
	ctx, span := infra.StartSpan(ctx, "cron.dream_narrative")
	defer span.End()

	var parts []string
	parts = append(parts, fmt.Sprintf("Metrics: %d entries processed, %d facts extracted, %d facts written to memory, %d contexts synthesized.",
		in.EntriesProcessed, in.FactsExtracted, in.FactsWritten, in.ContextsSynthesized))
	if len(in.PersonaFacts) > 0 {
		parts = append(parts, "New identity/persona facts committed:\n"+strings.Join(in.PersonaFacts, "\n"))
	}
	if in.EvolutionAudit != nil {
		parts = append(parts, "Cognitive Engineer audit summary: "+in.EvolutionAudit.Summary)
		if len(in.EvolutionAudit.Facts) > 0 {
			parts = append(parts, "Friction/gaps: "+strings.Join(in.EvolutionAudit.Facts, "; "))
		}
		if len(in.EvolutionAudit.Entities) > 0 {
			parts = append(parts, "Recommended changes: "+strings.Join(in.EvolutionAudit.Entities, "; "))
		}
		if len(in.EvolutionAudit.EngineerQuestions) > 0 {
			parts = append(parts, "Questions for the developer (build these tools or fix these issues): "+strings.Join(in.EvolutionAudit.EngineerQuestions, "\n"))
		}
	}

	userPrompt := utils.WrapAsUserData(utils.SanitizePrompt(strings.Join(parts, "\n\n")))
	narrative, err := infra.GenerateContentSimple(ctx, prompts.DreamStoryTemplate(), userPrompt, app.Config(), &infra.GenConfig{
		MaxOutputTokens: 4096, // enough for full morning readout (themes, facts, open loops, tool asks)
		ModelOverride:   app.DreamerModel(),
	})
	if err != nil {
		return err
	}
	narrative = strings.TrimSpace(narrative)
	if narrative == "" {
		infra.LoggerFrom(ctx).Warn("dream narrative was empty, skipping write")
		return nil
	}

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	_, err = client.Collection(systemCollection).Doc(latestDreamDoc).Set(ctx, map[string]interface{}{
		"narrative": narrative,
		"timestamp": now,
		"unread":    true,
	})
	if err != nil {
		return err
	}
	infra.LoggerFrom(ctx).Info("dream narrative written", "len", len(narrative))
	return nil
}

func extractDreamerPersonaFacts(outputs []*SpecialistOutput, domains []Domain) []string {
	const personaPrefix = "PERSONA: "
	var personaFacts []string
	for i, d := range domains {
		if d != DomainSelfModel || outputs[i] == nil {
			continue
		}
		for _, f := range outputs[i].Facts {
			f = strings.TrimSpace(f)
			if strings.HasPrefix(f, personaPrefix) {
				personaFacts = append(personaFacts, strings.TrimSpace(strings.TrimPrefix(f, personaPrefix)))
			}
		}
		break
	}
	return personaFacts
}

func loadDreamerInputs(ctx context.Context) (*DreamerInputs, error) {
	cutoff := time.Now().Add(-24 * time.Hour)
	startDate := cutoff.Format("2006-01-02")
	endDate := time.Now().Format("2006-01-02")
	entries, err := journal.GetEntriesByDateRange(ctx, startDate, endDate, 200)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return &DreamerInputs{}, nil
	}
	var lines []string
	for _, e := range entries {
		lines = append(lines, fmt.Sprintf("[%s] %s", e.Timestamp, e.Content))
	}
	journalContext := strings.Join(lines, "\n")
	if len(journalContext) > 6000 {
		journalContext = utils.TruncateToMaxBytes(journalContext, 6000) + "\n... (truncated)"
	}
	entryUUIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		entryUUIDs = append(entryUUIDs, e.UUID)
	}
	recentQueriesText := ""
	if queries, qErr := journal.GetRecentQueries(ctx, 50); qErr == nil && len(queries) > 0 {
		var qLines []string
		for _, q := range queries {
			ts := q.Timestamp
			if len(ts) > 16 {
				ts = ts[:16]
			}
			qLines = append(qLines, fmt.Sprintf("[%s] Q: %s\n  A: %s", ts, q.Question, utils.TruncateString(q.Answer, 200)))
		}
		recentQueriesText = strings.Join(qLines, "\n\n")
		if len(recentQueriesText) > 8000 {
			recentQueriesText = utils.TruncateToMaxBytes(recentQueriesText, 8000) + "\n... (truncated)"
		}
	}
	return &DreamerInputs{
		JournalContext:    journalContext,
		EntryUUIDs:        entryUUIDs,
		RecentQueriesText: recentQueriesText,
	}, nil
}

func generateEmbedding(ctx context.Context, text string, taskType ...string) ([]float32, error) {
	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("no app config for embedding")
	}
	return infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, text, taskType...)
}

// RunDreamer consolidates the last 24h of journal entries into semantic memory.
func RunDreamer(ctx context.Context, app *infra.App) (*DreamerResult, error) {
	ctx = infra.WithApp(ctx, app)
	ctx, span := infra.StartSpan(ctx, "cron.dreamer")
	defer span.End()

	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}

	tDreamStart := time.Now()
	infra.LoggerFrom(ctx).Info("dreamer starting", "window", "24h")

	inputs, err := loadDreamerInputs(ctx)
	if err != nil {
		span.RecordError(err)
		infra.LoggerFrom(ctx).Error("dreamer fetch entries failed", "error", err)
		return nil, err
	}
	if len(inputs.EntryUUIDs) == 0 {
		infra.LoggerFrom(ctx).Info("dreamer: no entries in last 24h")
		return &DreamerResult{EntriesProcessed: 0}, nil
	}

	infra.LoggerFrom(ctx).Info("dreamer fetched entries", "count", len(inputs.EntryUUIDs), "fetch_ms", time.Since(tDreamStart).Milliseconds())
	infra.LoggerFrom(ctx).Info("dreamer journal", "total_chars", len(inputs.JournalContext), "journal", inputs.JournalContext)

	journalContext := inputs.JournalContext
	entryUUIDs := inputs.EntryUUIDs
	recentQueriesText := inputs.RecentQueriesText

	domains := []Domain{DomainRelationship, DomainWork, DomainTask, DomainThought, DomainSelfModel}
	tSpecialistsStart := time.Now()
	input := &SpecialistInput{
		UserMessage: "Consolidate the last 24 hours of journal entries. Extract GOLD: people, projects, events, preferences, milestones, who is involved in what. Discard GRAVEL only: trivial one-off errands (buy milk, pick up package) with no lasting significance.",
		Context:     "IGNORE system commands and queries (list contexts, delete, what is X, how old is). Focus on SUBSTANTIVE statements: party planning, people mentioned, relationships, plans. 'Gloria's birthday party April 18th', 'Lindsay confirmed she's coming', 'Clarissa will help with cake' are facts. Extract 1-10 facts per domain.",
		Journal:     journalContext,
	}
	outputs := make([]*SpecialistOutput, len(domains))
	var impactedContexts []string
	var queryAnalysis string
	contextUUIDs := make(map[string]struct{})

	dreamerModel := app.DreamerModel()

	g, gctx := errgroup.WithContext(ctx)
	for i, domain := range domains {
		idx, d := i, domain
		g.Go(func() error {
			infra.LoggerFrom(ctx).Info("dreamer specialist start", "domain", d)
			out, err := RunSpecialist(gctx, d, input, dreamerModel)
			if err != nil {
				infra.LoggerFrom(ctx).Warn("dreamer specialist failed", "domain", d, "error", err)
				return err
			}
			outputs[idx] = out
			infra.LoggerFrom(ctx).Info("dreamer specialist done", "domain", d, "facts", len(out.Facts))
			return nil
		})
	}
	g.Go(func() error {
		ctxs, err := RunContextExtractor(gctx, journalContext)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer context extractor failed", "error", err)
			return nil
		}
		impactedContexts = ctxs
		return nil
	})
	g.Go(func() error {
		analysis, err := RunQueryAnalyzer(gctx, recentQueriesText)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer query analyzer failed", "error", err)
			return nil
		}
		queryAnalysis = analysis
		return nil
	})

	err = g.Wait()
	if err != nil {
		anyOk := false
		for i := 0; i < len(domains); i++ {
			if outputs[i] != nil {
				anyOk = true
				break
			}
		}
		if !anyOk {
			span.RecordError(err)
			return nil, fmt.Errorf("dreamer: all specialists failed: %w", err)
		}
		infra.LoggerFrom(ctx).Warn("dreamer some specialists or tasks failed", "error", err)
	}

	for _, name := range impactedContexts {
		ctxUUID, e := memory.EnsureContextExists(ctx, name)
		if e != nil {
			infra.LoggerFrom(ctx).Warn("dreamer ensure context failed", "name", name, "error", e)
			continue
		}
		contextUUIDs[ctxUUID] = struct{}{}
	}
	for uuid := range contextUUIDs {
		if e := memory.TouchContextBatch(ctx, uuid, entryUUIDs, 0.05); e != nil {
			infra.LoggerFrom(ctx).Warn("dreamer touch context batch failed", "context_uuid", uuid, "error", e)
		}
	}

	if queryAnalysis != "" {
		thoughtContent := "Query analysis: " + queryAnalysis
		if _, e := memory.UpsertSemanticMemory(ctx, thoughtContent, "thought", "selfmodel", 0.9, nil, nil); e != nil {
			infra.LoggerFrom(ctx).Warn("dreamer save query analysis thought failed", "error", e)
		} else {
			infra.LoggerFrom(ctx).Info("dreamer saved query analysis thought", "len", len(queryAnalysis))
		}
	}

	infra.LoggerFrom(ctx).Info("dreamer specialists complete", "specialists_ms", time.Since(tSpecialistsStart).Milliseconds())

	totalFacts := 0
	for i := range domains {
		if outputs[i] != nil {
			totalFacts += len(outputs[i].Facts)
		}
	}
	infra.LoggerFrom(ctx).Info("dreamer starting consolidation", "total_facts", totalFacts)

	merged := mergeDreamerFacts(ctx, domains, outputs)
	infra.LoggerFrom(ctx).Info("dreamer merge complete", "before", totalFacts, "after", len(merged), "msg", fmt.Sprintf("dreamer merged %d facts into %d", totalFacts, len(merged)))

	written, _ := dreamerWriteMergedFacts(ctx, merged, inputs.EntryUUIDs)

	if err = RunGapDetection(ctx, inputs.JournalContext, inputs.EntryUUIDs); err != nil {
		infra.LoggerFrom(ctx).Warn("dreamer gap detection failed", "error", err)
	} else {
		infra.LoggerFrom(ctx).Info("dreamer gap detection completed")
	}

	synthesized, skippedLazy, _ := dreamerSynthesizeContexts(ctx, contextUUIDs)
	if skippedLazy > 0 {
		infra.LoggerFrom(ctx).Info("dreamer synthesis skipped (lazy)", "count", skippedLazy)
	}

	personaFacts := extractDreamerPersonaFacts(outputs, domains)
	if len(personaFacts) > 0 {
		if err = RunProfileSynthesis(ctx, personaFacts); err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer profile synthesis failed", "error", err)
		} else {
			infra.LoggerFrom(ctx).Info("dreamer profile synthesis completed", "persona_facts", len(personaFacts))
		}
	}

	var evolutionAudit *EvolutionAuditOutput
	if audit, synErr := RunEvolutionSynthesis(ctx, journalContext); synErr != nil {
		infra.LoggerFrom(ctx).Warn("dreamer evolution synthesis failed", "error", synErr)
	} else {
		evolutionAudit = audit
		infra.LoggerFrom(ctx).Info("dreamer evolution synthesis completed")
	}

	// Generate and store the Dream Narrative (morning readout) for the user.
	if err := writeDreamNarrative(ctx, app, &dreamNarrativeInput{
		EntriesProcessed:    len(entryUUIDs),
		FactsExtracted:      totalFacts,
		FactsWritten:        written,
		ContextsSynthesized: synthesized,
		PersonaFacts:        personaFacts,
		EvolutionAudit:      evolutionAudit,
	}); err != nil {
		infra.LoggerFrom(ctx).Warn("dreamer narrative failed", "error", err)
	}

	infra.LoggerFrom(ctx).Info("dreamer completed", "entries_processed", len(entryUUIDs), "facts_extracted", totalFacts, "facts_written", written, "contexts_synthesized", synthesized, "total_ms", time.Since(tDreamStart).Milliseconds(), "msg", fmt.Sprintf("dreamer completed: %d entries -> %d extracted -> %d written, %d contexts synthesized in %dms", len(entryUUIDs), totalFacts, written, synthesized, time.Since(tDreamStart).Milliseconds()))
	span.SetAttributes(map[string]string{
		"entries":     fmt.Sprintf("%d", len(entryUUIDs)),
		"written":     fmt.Sprintf("%d", written),
		"synthesized": fmt.Sprintf("%d", synthesized),
	})
	return &DreamerResult{
		EntriesProcessed:    len(entryUUIDs),
		FactsExtracted:      totalFacts,
		FactsWritten:        written,
		ContextsSynthesized: synthesized,
	}, nil
}
