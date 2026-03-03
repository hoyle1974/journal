package jot

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"golang.org/x/sync/errgroup"
	"google.golang.org/api/iterator"
)

const (
	JanitorWeightThreshold   = 0.2
	JanitorStaleDays         = 30
	PulseStaleDays           = 14
	PulseImportanceThreshold = 0.7
	dreamerMergeSimilarity   = 0.93 // cosine similarity above this = same fact
	dreamerBaseWeight        = 0.7
	dreamerWeightBoostPerDup = 0.1

	dreamerSynthesisNewLogsThreshold = 3  // run synthesis if this many new entries since last
	dreamerSynthesisStaleHours       = 48 // high-significance contexts re-synthesize if older than this
)

// PulseResult holds the outcome of a pulse audit run.
type PulseResult struct {
	StaleNodes []string
	Signals    int
}

// mergedFact is one fact after consolidation (canonical content + aggregated weight).
type mergedFact struct {
	Content  string
	NodeType string
	Domain   string
	Weight   float64
}

// cosineSimilarity returns the cosine similarity of two vectors (0..1).
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

// mergeDreamerFacts flattens specialist outputs, dedupes by embedding similarity, and returns one merged fact per group.
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
			vec, err := GenerateEmbedding(ctx, f)
			if err != nil {
				LoggerFrom(ctx).Debug("dreamer merge skip embedding", "fact", truncateString(f, 40), "error", err)
				continue
			}
			flat = append(flat, factWithMeta{fact: f, domain: domain, vec: vec})
		}
	}
	if len(flat) == 0 {
		return nil
	}
	// Group by similarity: each fact goes into first group it matches, or new group
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
			if sim >= dreamerMergeSimilarity {
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
		// Canonical: longest fact in group
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
		weight := dreamerBaseWeight + dreamerWeightBoostPerDup*float64(len(g)-1)
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

// DreamerResult holds the outcome of a dream run for diagnostics.
type DreamerResult struct {
	EntriesProcessed     int
	FactsExtracted       int
	FactsWritten         int
	ContextsSynthesized  int
}

// shouldSynthesizeContext returns true if we should run the LLM for this context (lazy loading).
func shouldSynthesizeContext(meta *ContextMetadata) bool {
	neverSynthesized := meta.LastSynthesizedAt == "" && meta.SourceEntryCountAtSynthesis == 0
	if neverSynthesized {
		return true
	}
	newLogs := len(meta.SourceEntries) > meta.SourceEntryCountAtSynthesis+dreamerSynthesisNewLogsThreshold
	if newLogs {
		return true
	}
	if meta.Relevance < PulseImportanceThreshold {
		return false
	}
	if meta.LastSynthesizedAt == "" {
		return true
	}
	lastSynth, err := time.Parse(time.RFC3339, meta.LastSynthesizedAt)
	if err != nil {
		return true
	}
	return time.Since(lastSynth) > dreamerSynthesisStaleHours*time.Hour
}

// batchToSpecialistOutputs converts BatchCommitteeOutput to []*SpecialistOutput for mergeDreamerFacts and persona extraction.
func batchToSpecialistOutputs(batch *BatchCommitteeOutput, domains []Domain) []*SpecialistOutput {
	outputs := make([]*SpecialistOutput, len(domains))
	for i, d := range domains {
		key := string(d)
		facts := batch.Domains[key]
		if d == DomainSelfModel && len(batch.IdentityMarkers) > 0 {
			facts = append(facts, batch.IdentityMarkers...)
		}
		outputs[i] = &SpecialistOutput{Domain: d, Facts: facts}
	}
	return outputs
}

// dreamerInputs holds loaded data for a dream run (entries, journal text, UUIDs, recent queries).
type dreamerInputs struct {
	entries           []Entry
	journalContext    string
	entryUUIDs        []string
	recentQueriesText string
}

// dreamerLoadInputs fetches entries for the last 24h and builds journal context, entry UUIDs, and recent queries text.
func dreamerLoadInputs(ctx context.Context) (*dreamerInputs, error) {
	cutoff := time.Now().Add(-24 * time.Hour)
	startDate := cutoff.Format("2006-01-02")
	endDate := time.Now().Format("2006-01-02")
	entries, err := GetEntriesByDateRange(ctx, startDate, endDate, 200)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return &dreamerInputs{entries: entries}, nil
	}
	var lines []string
	for _, e := range entries {
		lines = append(lines, fmt.Sprintf("[%s] %s", e.Timestamp, e.Content))
	}
	journalContext := strings.Join(lines, "\n")
	if len(journalContext) > 6000 {
		journalContext = truncateToMaxBytes(journalContext, 6000) + "\n... (truncated)"
	}
	entryUUIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		entryUUIDs = append(entryUUIDs, e.UUID)
	}
	recentQueriesText := ""
	if queries, qErr := GetRecentQueries(ctx, 50); qErr == nil && len(queries) > 0 {
		var qLines []string
		for _, q := range queries {
			ts := q.Timestamp
			if len(ts) > 16 {
				ts = ts[:16]
			}
			qLines = append(qLines, fmt.Sprintf("[%s] Q: %s\n  A: %s", ts, q.Question, truncateString(q.Answer, 200)))
		}
		recentQueriesText = strings.Join(qLines, "\n\n")
		if len(recentQueriesText) > 8000 {
			recentQueriesText = truncateToMaxBytes(recentQueriesText, 8000) + "\n... (truncated)"
		}
	}
	return &dreamerInputs{
		entries:           entries,
		journalContext:    journalContext,
		entryUUIDs:        entryUUIDs,
		recentQueriesText: recentQueriesText,
	}, nil
}

// dreamerWriteMergedFacts upserts merged facts to semantic memory and returns the number written.
func dreamerWriteMergedFacts(ctx context.Context, merged []mergedFact) (written int, err error) {
	for _, m := range merged {
		if _, err = UpsertSemanticMemory(ctx, m.Content, m.NodeType, m.Domain, m.Weight, nil, nil); err != nil {
			LoggerFrom(ctx).Warn("dreamer upsert failed", "domain", m.Domain, "fact", truncateString(m.Content, 50), "error", err)
			continue
		}
		written++
		LoggerFrom(ctx).Info("dreamer wrote fact", "domain", m.Domain, "fact", truncateString(m.Content, 60), "n", written, "total", len(merged))
	}
	return written, nil
}

// dreamerSynthesizeContexts synthesizes or touches each context in contextUUIDs; returns count synthesized and count skipped (lazy).
func dreamerSynthesizeContexts(ctx context.Context, contextUUIDs map[string]struct{}) (synthesized, skippedLazy int, err error) {
	var meta *ContextMetadata
	for uuid := range contextUUIDs {
		meta, err = GetContextMetadata(ctx, uuid)
		if err != nil {
			LoggerFrom(ctx).Warn("dreamer get context metadata failed", "context_uuid", uuid, "error", err)
			continue
		}
		if !shouldSynthesizeContext(meta) {
			skippedLazy++
			if err = TouchContext(ctx, uuid, nil, 0); err != nil {
				LoggerFrom(ctx).Debug("dreamer touch context skipped", "context_uuid", uuid, "error", err)
			}
			continue
		}
		if err = SynthesizeContext(ctx, uuid); err != nil {
			LoggerFrom(ctx).Warn("dreamer context synthesis failed", "context_uuid", uuid, "error", err)
			continue
		}
		synthesized++
	}
	return synthesized, skippedLazy, nil
}

// extractDreamerPersonaFacts returns PERSONA-prefixed facts from the SelfModel specialist output.
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
func RunDreamer(ctx context.Context) (*DreamerResult, error) {
	ctx, span := StartSpan(ctx, "cron.dreamer")
	defer span.End()

	tDreamStart := time.Now()
	LoggerFrom(ctx).Info("dreamer starting", "window", "24h")

	inputs, err := dreamerLoadInputs(ctx)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("dreamer fetch entries failed", "error", err)
		return nil, err
	}
	entries := inputs.entries
	if len(entries) == 0 {
		LoggerFrom(ctx).Info("dreamer: no entries in last 24h")
		return &DreamerResult{EntriesProcessed: 0}, nil
	}

	LoggerFrom(ctx).Info("dreamer fetched entries", "count", len(entries), "fetch_ms", time.Since(tDreamStart).Milliseconds())
	LoggerFrom(ctx).Info("dreamer journal", "total_chars", len(inputs.journalContext), "journal", inputs.journalContext)

	journalContext := inputs.journalContext
	entryUUIDs := inputs.entryUUIDs
	recentQueriesText := inputs.recentQueriesText

	domains := []Domain{DomainRelationship, DomainWork, DomainTask, DomainThought, DomainSelfModel}
	tSpecialistsStart := time.Now()
	var outputs []*SpecialistOutput
	contextUUIDs := make(map[string]struct{})

	if getEnv("DREAMER_UNIFIED_COMMITTEE", "true") != "false" {
		// Single "committee dispatch" call instead of 5 parallel specialists (includes query analysis when recent queries provided).
		var batch *BatchCommitteeOutput
		batch, err = RunUnifiedCommittee(ctx, journalContext, recentQueriesText)
		if err != nil {
			span.RecordError(err)
			LoggerFrom(ctx).Warn("dreamer unified committee failed, falling back to per-domain specialists", "error", err)
			// Fall back to per-domain specialists so the dream still runs (unified call can return empty/truncated JSON).
			input := &SpecialistInput{
				UserMessage: "Consolidate the last 24 hours of journal entries. Extract GOLD: people, projects, events, preferences, milestones, who is involved in what. Discard GRAVEL only: trivial one-off errands (buy milk, pick up package) with no lasting significance.",
				Context:     "IGNORE system commands and queries (list contexts, delete, show todo, what is X, how old is). Focus on SUBSTANTIVE statements: party planning, people mentioned, relationships, plans. 'Gloria's birthday party April 18th', 'Lindsay confirmed she's coming', 'Clarissa will help with cake' are facts. Extract 1-10 facts per domain.",
				Journal:     journalContext,
			}
			outputs = make([]*SpecialistOutput, len(domains))
			g, gctx := errgroup.WithContext(ctx)
			for i, domain := range domains {
				idx, d := i, domain
				g.Go(func() error {
					LoggerFrom(ctx).Info("dreamer specialist start", "domain", d, "msg", fmt.Sprintf("dreamer starting %s (unified fallback)", d))
					out, err := RunSpecialist(gctx, d, input, DreamerModel)
					if err != nil {
						LoggerFrom(ctx).Warn("dreamer specialist failed", "domain", d, "error", err)
						return err
					}
					outputs[idx] = out
					LoggerFrom(ctx).Info("dreamer specialist done", "domain", d, "facts", len(out.Facts))
					return nil
				})
			}
			if err = g.Wait(); err != nil {
				anyOk := false
				for _, o := range outputs {
					if o != nil {
						anyOk = true
						break
					}
				}
				if !anyOk {
					span.RecordError(err)
					return nil, fmt.Errorf("dreamer: unified committee failed and all specialists failed: %w", err)
				}
				LoggerFrom(ctx).Warn("dreamer some specialists failed after unified fallback", "error", err)
			}
			LoggerFrom(ctx).Info("dreamer completed via specialist fallback", "specialists_ms", time.Since(tSpecialistsStart).Milliseconds())
		} else {
			outputs = batchToSpecialistOutputs(batch, domains)
		LoggerFrom(ctx).Info("dreamer unified committee complete", "msg", "dreamer single committee call finished", "specialists_ms", time.Since(tSpecialistsStart).Milliseconds())

		// Save query analysis (semantic clusters, knowledge gaps, curiosity trends) as high-significance thought in selfmodel
		if batch.QueryAnalysis != "" {
			thoughtContent := "Query analysis: " + batch.QueryAnalysis
			if _, err = UpsertSemanticMemory(ctx, thoughtContent, "thought", "selfmodel", 0.9, nil, nil); err != nil {
				LoggerFrom(ctx).Warn("dreamer save query analysis thought failed", "error", err)
			} else {
				LoggerFrom(ctx).Info("dreamer saved query analysis thought", "len", len(batch.QueryAnalysis))
			}
		}

		// Derive context UUIDs from committee's ImpactedContexts (no per-entry LLM calls).
		var ctxUUID string
		for _, name := range batch.ImpactedContexts {
			ctxUUID, err = EnsureContextExists(ctx, name)
			if err != nil {
				LoggerFrom(ctx).Warn("dreamer ensure context failed", "name", name, "error", err)
				continue
			}
			contextUUIDs[ctxUUID] = struct{}{}
		}
		// Batch-link all 24h entries to each impacted context.
		for uuid := range contextUUIDs {
			if err = TouchContextBatch(ctx, uuid, entryUUIDs, 0.05); err != nil {
				LoggerFrom(ctx).Warn("dreamer touch context batch failed", "context_uuid", uuid, "error", err)
			}
		}
		}
	} else {
		input := &SpecialistInput{
			UserMessage: "Consolidate the last 24 hours of journal entries. Extract GOLD: people, projects, events, preferences, milestones, who is involved in what. Discard GRAVEL only: trivial one-off errands (buy milk, pick up package) with no lasting significance.",
			Context:     "IGNORE system commands and queries (list contexts, delete, show todo, what is X, how old is). Focus on SUBSTANTIVE statements: party planning, people mentioned, relationships, plans. 'Gloria's birthday party April 18th', 'Lindsay confirmed she's coming', 'Clarissa will help with cake' are facts. Extract 1-10 facts per domain.",
			Journal:     journalContext,
		}
		outputs = make([]*SpecialistOutput, len(domains))
		g, gctx := errgroup.WithContext(ctx)
		for i, domain := range domains {
			idx, d := i, domain
			g.Go(func() error {
				LoggerFrom(ctx).Info("dreamer specialist start", "domain", d, "msg", fmt.Sprintf("dreamer starting %s", d))
				out, err := RunSpecialist(gctx, d, input, DreamerModel)
				if err != nil {
					LoggerFrom(ctx).Warn("dreamer specialist failed", "domain", d, "error", err, "msg", fmt.Sprintf("dreamer %s failed: %v", d, err))
					return err
				}
				outputs[idx] = out
				LoggerFrom(ctx).Info("dreamer specialist done", "domain", d, "facts", len(out.Facts), "msg", fmt.Sprintf("dreamer %s done: %d facts", d, len(out.Facts)))
				return nil
			})
		}
		if err = g.Wait(); err != nil {
			anyOk := false
			for _, o := range outputs {
				if o != nil {
					anyOk = true
					break
				}
			}
			if !anyOk {
				span.RecordError(err)
				return nil, fmt.Errorf("dreamer: all specialists failed: %w", err)
			}
			LoggerFrom(ctx).Warn("dreamer some specialists failed", "error", err)
		}
		LoggerFrom(ctx).Info("dreamer specialists complete", "msg", "dreamer all specialists finished", "specialists_ms", time.Since(tSpecialistsStart).Milliseconds())
	}

	// Flatten and count facts before merge
	totalFacts := 0
	for i := range domains {
		if outputs[i] != nil {
			totalFacts += len(outputs[i].Facts)
		}
	}
	LoggerFrom(ctx).Info("dreamer starting consolidation", "total_facts", totalFacts)

	// Consolidation merge: dedupe by embedding similarity, aggregate weight, then write
	merged := mergeDreamerFacts(ctx, domains, outputs)
	LoggerFrom(ctx).Info("dreamer merge complete", "before", totalFacts, "after", len(merged), "msg", fmt.Sprintf("dreamer merged %d facts into %d", totalFacts, len(merged)))

	written, _ := dreamerWriteMergedFacts(ctx, merged)

	synthesized, skippedLazy, _ := dreamerSynthesizeContexts(ctx, contextUUIDs)
	if skippedLazy > 0 {
		LoggerFrom(ctx).Info("dreamer synthesis skipped (lazy)", "count", skippedLazy)
	}

	personaFacts := extractDreamerPersonaFacts(outputs, domains)
	if len(personaFacts) > 0 {
		if err = RunProfileSynthesis(ctx, personaFacts); err != nil {
			LoggerFrom(ctx).Warn("dreamer profile synthesis failed", "error", err)
		} else {
			LoggerFrom(ctx).Info("dreamer profile synthesis completed", "persona_facts", len(personaFacts))
		}
	}

	// Nightly system evolution audit: Cognitive Engineer analyzes recent queries and journal for friction.
	if err = RunEvolutionSynthesis(ctx, journalContext); err != nil {
		LoggerFrom(ctx).Warn("dreamer evolution synthesis failed", "error", err)
	} else {
		LoggerFrom(ctx).Info("dreamer evolution synthesis completed")
	}

	LoggerFrom(ctx).Info("dreamer completed", "entries_processed", len(entries), "facts_extracted", totalFacts, "facts_written", written, "contexts_synthesized", synthesized, "total_ms", time.Since(tDreamStart).Milliseconds(), "msg", fmt.Sprintf("dreamer completed: %d entries -> %d extracted -> %d written, %d contexts synthesized in %dms", len(entries), totalFacts, written, synthesized, time.Since(tDreamStart).Milliseconds()))
	span.SetAttributes(map[string]string{
		"entries":     fmt.Sprintf("%d", len(entries)),
		"written":     fmt.Sprintf("%d", written),
		"synthesized": fmt.Sprintf("%d", synthesized),
	})
	return &DreamerResult{
		EntriesProcessed:    len(entries),
		FactsExtracted:      totalFacts,
		FactsWritten:        written,
		ContextsSynthesized: synthesized,
	}, nil
}

// RunProfileSynthesis merges new persona facts into the permanent user_profile context node.
func RunProfileSynthesis(ctx context.Context, personaFacts []string) error {
	ctx, span := StartSpan(ctx, "cron.profile_synthesis")
	defer span.End()

	if len(personaFacts) == 0 {
		return nil
	}

	node, _, err := FindContextByName(ctx, "user_profile")
	if err != nil || node == nil {
		return fmt.Errorf("user_profile node not found: %w", err)
	}

	userPrompt := fmt.Sprintf("Current Profile:\n%s\n\nNew Identity Markers:\n%s",
		WrapAsUserData(node.Content), WrapAsUserData(strings.Join(personaFacts, "\n")))

	newProfile, err := GenerateContentSimple(ctx, IdentityArchitectSystemPrompt, userPrompt, &GenConfig{MaxOutputTokens: 1024, ModelOverride: DreamerModel})
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
		journalForEvolution = truncateToMaxBytes(journalSummary, 2000) + "\n... (truncated)"
	} else {
		journalForEvolution = journalSummary
	}

	audit, err := RunEvolutionAudit(ctx, queriesText, journalForEvolution)
	if err != nil {
		return err
	}

	node, _, err := FindContextByName(ctx, "system_evolution")
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
		LoggerFrom(ctx).Info("pulse audit flagged node", "id", nodeID, "content", truncateString(content, 40))
	}

	span.SetAttributes(map[string]string{
		"stale_nodes": fmt.Sprintf("%d", len(result.StaleNodes)),
		"signals":     fmt.Sprintf("%d", result.Signals),
	})
	return result, nil
}
