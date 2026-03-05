package agent

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackstrohm/jot/pkg/infra"
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

func mergeDreamerFacts(ctx context.Context, env DreamerEnv, domains []Domain, outputs []*SpecialistOutput) []mergedFact {
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
			vec, err := env.GenerateEmbedding(ctx, f)
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

func shouldSynthesizeContext(meta *ContextMetadata) bool {
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

func dreamerWriteMergedFacts(ctx context.Context, env DreamerEnv, merged []mergedFact, entryUUIDs []string) (written int, err error) {
	for _, m := range merged {
		if _, err = env.UpsertSemanticMemory(ctx, m.Content, m.NodeType, m.Domain, m.Weight, nil, entryUUIDs); err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer upsert failed", "domain", m.Domain, "fact", utils.TruncateString(m.Content, 50), "error", err)
			continue
		}
		written++
		infra.LoggerFrom(ctx).Info("dreamer wrote fact", "domain", m.Domain, "fact", utils.TruncateString(m.Content, 60), "n", written, "total", len(merged))
	}
	return written, nil
}

func dreamerSynthesizeContexts(ctx context.Context, env DreamerEnv, contextUUIDs map[string]struct{}) (synthesized, skippedLazy int, err error) {
	for uuid := range contextUUIDs {
		meta, err := env.GetContextMetadata(ctx, uuid)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer get context metadata failed", "context_uuid", uuid, "error", err)
			continue
		}
		if !shouldSynthesizeContext(meta) {
			skippedLazy++
			if err = env.TouchContext(ctx, uuid, 0); err != nil {
				infra.LoggerFrom(ctx).Debug("dreamer touch context skipped", "context_uuid", uuid, "error", err)
			}
			continue
		}
		if err = env.SynthesizeContext(ctx, uuid); err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer context synthesis failed", "context_uuid", uuid, "error", err)
			continue
		}
		synthesized++
	}
	return synthesized, skippedLazy, nil
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

// RunDreamer consolidates the last 24h of journal entries into semantic memory.
func RunDreamer(ctx context.Context, env DreamerEnv) (*DreamerResult, error) {
	ctx, span := infra.StartSpan(ctx, "cron.dreamer")
	defer span.End()

	tDreamStart := time.Now()
	infra.LoggerFrom(ctx).Info("dreamer starting", "window", "24h")

	inputs, err := env.LoadDreamerInputs(ctx)
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
		Context:     "IGNORE system commands and queries (list contexts, delete, show todo, what is X, how old is). Focus on SUBSTANTIVE statements: party planning, people mentioned, relationships, plans. 'Gloria's birthday party April 18th', 'Lindsay confirmed she's coming', 'Clarissa will help with cake' are facts. Extract 1-10 facts per domain.",
		Journal:     journalContext,
	}
	outputs := make([]*SpecialistOutput, len(domains))
	var impactedContexts []string
	var queryAnalysis string
	contextUUIDs := make(map[string]struct{})

	app := infra.GetApp(ctx)
	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}
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
		ctxUUID, e := env.EnsureContextExists(ctx, name)
		if e != nil {
			infra.LoggerFrom(ctx).Warn("dreamer ensure context failed", "name", name, "error", e)
			continue
		}
		contextUUIDs[ctxUUID] = struct{}{}
	}
	for uuid := range contextUUIDs {
		if e := env.TouchContextBatch(ctx, uuid, entryUUIDs, 0.05); e != nil {
			infra.LoggerFrom(ctx).Warn("dreamer touch context batch failed", "context_uuid", uuid, "error", e)
		}
	}

	if queryAnalysis != "" {
		thoughtContent := "Query analysis: " + queryAnalysis
		if _, e := env.UpsertSemanticMemory(ctx, thoughtContent, "thought", "selfmodel", 0.9, nil, nil); e != nil {
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

	merged := mergeDreamerFacts(ctx, env, domains, outputs)
	infra.LoggerFrom(ctx).Info("dreamer merge complete", "before", totalFacts, "after", len(merged), "msg", fmt.Sprintf("dreamer merged %d facts into %d", totalFacts, len(merged)))

	written, _ := dreamerWriteMergedFacts(ctx, env, merged, inputs.EntryUUIDs)

	if err = env.RunGapDetection(ctx, inputs.JournalContext, inputs.EntryUUIDs); err != nil {
		infra.LoggerFrom(ctx).Warn("dreamer gap detection failed", "error", err)
	} else {
		infra.LoggerFrom(ctx).Info("dreamer gap detection completed")
	}

	synthesized, skippedLazy, _ := dreamerSynthesizeContexts(ctx, env, contextUUIDs)
	if skippedLazy > 0 {
		infra.LoggerFrom(ctx).Info("dreamer synthesis skipped (lazy)", "count", skippedLazy)
	}

	personaFacts := extractDreamerPersonaFacts(outputs, domains)
	if len(personaFacts) > 0 {
		if err = env.RunProfileSynthesis(ctx, personaFacts); err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer profile synthesis failed", "error", err)
		} else {
			infra.LoggerFrom(ctx).Info("dreamer profile synthesis completed", "persona_facts", len(personaFacts))
		}
	}

	if err = env.RunEvolutionSynthesis(ctx, journalContext); err != nil {
		infra.LoggerFrom(ctx).Warn("dreamer evolution synthesis failed", "error", err)
	} else {
		infra.LoggerFrom(ctx).Info("dreamer evolution synthesis completed")
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
