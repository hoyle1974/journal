package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/gdoc"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/system"
	"github.com/jackstrohm/jot/pkg/task"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
	"golang.org/x/sync/errgroup"
	"google.golang.org/genai"
)

const (
	DreamerMergeSimilarity         = 0.93 // cosine similarity above this = same fact
	DreamerBaseWeight              = 0.7
	DreamerWeightBoostPerDup      = 0.1
	DreamerSynthesisNewLogsThreshold = 3  // run synthesis if this many new entries since last
	DreamerSynthesisStaleHours    = 48   // high-significance contexts re-synthesize if older than this
	DreamerTaskPhaseMaxIterations = 5   // max tool-call rounds in dreamer task phase
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

// DreamerProgress is called during a dream run to report phase and log lines (e.g. for async status in Firestore).
type DreamerProgress interface {
	OnPhase(ctx context.Context, phase string)
	OnLog(ctx context.Context, msg string)
}

// RunDreamerOpts optionally supplies a run ID and progress reporter for async dream runs.
type RunDreamerOpts struct {
	RunID    string
	Progress DreamerProgress
}

type mergedFact struct {
	Content  string
	NodeType string
	Domain   string
	Weight   float64
	Vector   []float32 // precomputed embedding; reused during upsert to avoid a second API call
}

// discussResult holds the outcome of a single specialist's colloquium turn.
type discussResult struct {
	msg    string
	isDone bool
}

func mergeDreamerFacts(ctx context.Context, app *infra.App, domains []Domain, outputs []*SpecialistOutput) []mergedFact {
	type factWithMeta struct {
		fact   string
		domain Domain
		vec    []float32
	}

	// Collect all non-empty facts first so we can batch-embed them in one request.
	type pendingFact struct {
		fact   string
		domain Domain
	}
	var pending []pendingFact
	for i, domain := range domains {
		out := outputs[i]
		if out == nil {
			continue
		}
		for _, f := range out.Facts {
			f = strings.TrimSpace(f)
			if f != "" {
				pending = append(pending, pendingFact{fact: f, domain: domain})
			}
		}
	}
	if len(pending) == 0 {
		return nil
	}

	texts := make([]string, len(pending))
	for i, p := range pending {
		texts[i] = p.fact
	}
	vecs, err := infra.GenerateEmbeddingsBatch(ctx, app.Config().GoogleCloudProject, texts, infra.EmbedTaskRetrievalDocument)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("dreamer merge batch embedding failed, skipping fact merge", "error", err)
		return nil
	}

	var flat []factWithMeta
	for i, p := range pending {
		if len(vecs[i]) == 0 {
			infra.LoggerFrom(ctx).Debug("dreamer merge skip empty embedding", "fact", p.fact)
			continue
		}
		flat = append(flat, factWithMeta{fact: p.fact, domain: p.domain, vec: vecs[i]})
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
			sim := utils.CosineSimilarity(flat[i].vec, flat[j].vec)
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
			Vector:   f.vec,
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

func dreamerWriteMergedFacts(ctx context.Context, app *infra.App, merged []mergedFact, entryUUIDs []string, progress DreamerProgress) (written int, err error) {
	total := len(merged)
	if total == 0 {
		return 0, nil
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for _, m := range merged {
		fact := m
		g.Go(func() error {
			if _, upsertErr := memory.UpsertSemanticMemoryPreembedded(gctx, app, fact.Content, fact.NodeType, fact.Domain, fact.Weight, nil, entryUUIDs, fact.Vector); upsertErr != nil {
				infra.LoggerFrom(gctx).Warn("dreamer upsert failed", "domain", fact.Domain, "fact", utils.TruncateString(fact.Content, 50), "error", upsertErr)
				return nil // soft error
			}
			mu.Lock()
			written++
			n := written
			mu.Unlock()
			infra.LoggerFrom(gctx).Info("dreamer wrote fact", "domain", fact.Domain, "fact", utils.TruncateString(fact.Content, 60), "n", n, "total", total)
			if progress != nil && (n%5 == 0 || n == total) {
				progress.OnLog(gctx, fmt.Sprintf("  Written %d/%d facts", n, total))
			}
			return nil
		})
	}
	_ = g.Wait()
	return written, nil
}

func dreamerSynthesizeContexts(ctx context.Context, app *infra.App, contextUUIDs map[string]struct{}) (synthesized, skippedLazy int, err error) {
	if len(contextUUIDs) == 0 {
		return 0, 0, nil
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for uuid := range contextUUIDs {
		u := uuid
		g.Go(func() error {
			meta, metaErr := memory.GetContextMetadata(gctx, app, u)
			if metaErr != nil {
				infra.LoggerFrom(gctx).Warn("dreamer get context metadata failed", "context_uuid", u, "error", metaErr)
				return nil
			}
			if !shouldSynthesizeContext(meta) {
				mu.Lock()
				skippedLazy++
				mu.Unlock()
				if touchErr := memory.TouchContext(gctx, app, u, nil, 0); touchErr != nil {
					infra.LoggerFrom(gctx).Debug("dreamer touch context skipped", "context_uuid", u, "error", touchErr)
				}
				return nil
			}
			if synthErr := memory.SynthesizeContext(gctx, app, u); synthErr != nil {
				infra.LoggerFrom(gctx).Warn("dreamer context synthesis failed", "context_uuid", u, "error", synthErr)
				return nil
			}
			mu.Lock()
			synthesized++
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	return synthesized, skippedLazy, nil
}

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
func writeDreamNarrative(ctx context.Context, app *infra.App, in *dreamNarrativeInput) (string, error) {
	ctx, span := infra.StartSpan(ctx, "cron.dream_narrative")
	defer span.End()

	var parts []string
	parts = append(parts, "### 🌅 System Consolidation Report")
	parts = append(parts, fmt.Sprintf("- **Processed:** %d entries", in.EntriesProcessed))
	parts = append(parts, fmt.Sprintf("- **Memory Delta:** +%d facts", in.FactsWritten))

	if len(in.PersonaFacts) > 0 {
		parts = append(parts, "\n### 🆔 Identity Updates")
		for _, f := range in.PersonaFacts {
			parts = append(parts, "- "+f)
		}
	}

	if in.EvolutionAudit != nil {
		parts = append(parts, "\n### 🛠️ System Health (Cognitive Engineer)")
		parts = append(parts, "> "+in.EvolutionAudit.Summary)
		if len(in.EvolutionAudit.Facts) > 0 {
			parts = append(parts, "Friction/gaps: "+strings.Join(in.EvolutionAudit.Facts, "; "))
		}
		if len(in.EvolutionAudit.Entities) > 0 {
			parts = append(parts, "Recommended changes: "+strings.Join(in.EvolutionAudit.Entities, "; "))
		}
		if len(in.EvolutionAudit.EngineerQuestions) > 0 {
			parts = append(parts, "Questions for the developer: "+strings.Join(in.EvolutionAudit.EngineerQuestions, "\n"))
		}
	}

	userPrompt := utils.WrapAsUserData(utils.SanitizePrompt(strings.Join(parts, "\n\n")))
	narrative, err := infra.GenerateContentSimple(ctx, app, prompts.DreamStoryTemplate(), userPrompt, app.Config(), &infra.GenConfig{
		MaxOutputTokens: 4096, // enough for full morning readout (themes, facts, open loops, tool asks)
		ModelOverride:   app.DreamerModel(),
	})
	if err != nil {
		return "", err
	}
	if narrative == "" {
		infra.LoggerFrom(ctx).Warn("dream narrative was empty, skipping write")
		return "", nil
	}

	now := time.Now().Format(time.RFC3339)
	if err := system.WriteLatestDream(ctx, app, narrative, now, true); err != nil {
		return "", err
	}
	infra.LoggerFrom(ctx).Info("dream narrative written", "len", len(narrative))
	return narrative, nil
}

const dreamerTaskPhaseSystemPrompt = `You are the dreamer's task phase. You have access to create_task, update_task_status, and search_tasks.
Given the journal context and current open todo roots below, create or update tasks when you infer something the user should track: follow-ups, deadlines, open loops, or commitments mentioned in the last 24h.
Use search_tasks first if you need to see existing tasks. Only create or update when clearly useful; do not create trivial or duplicate tasks. Reply briefly after tool use; no need to narrate.`

// runDreamerTaskPhase runs a short agentic loop with task tools so the dreamer can create/update tasks from the night's consolidation.
func runDreamerTaskPhase(ctx context.Context, app *infra.App, dreamerRunID string, journalContext string, entryUUIDs []string) {
	ctx, span := infra.StartSpan(ctx, "cron.dreamer_task_phase")
	defer span.End()

	taskToolDefs := tools.GetDefinitionsByCategory("task")
	if len(taskToolDefs) == 0 {
		infra.LoggerFrom(ctx).Debug("dreamer task phase: no task tools registered, skipping", "dreamer_run_id", dreamerRunID, "phase", "task_phase")
		return
	}

	useCompactTools := app != nil && app.Config() != nil && app.Config().UseCompactTools

	// Link created tasks to the most recent entry in the window (newest first).
	if len(entryUUIDs) > 0 {
		ctx = WithCurrentEntryUUID(ctx, entryUUIDs[0])
	}

	openRootsSummary := ""
	if roots, err := task.GetOpenRootTasks(ctx, app, 20); err == nil && len(roots) > 0 {
		openRootsSummary = "\n\nCurrent open todo list roots:\n" + task.FormatTasksForContext(roots, 1500)
	}

	journalPreview := journalContext
	if len(journalPreview) > 3500 {
		journalPreview = utils.TruncateToMaxBytes(journalPreview, 3500) + "\n... (truncated)"
	}
	userMsg := "Journal context from the last 24h (consolidated by the dreamer):\n" + utils.WrapAsUserData(utils.SanitizePrompt(journalPreview)) + openRootsSummary + "\n\nCreate or update tasks if anything here warrants tracking. Use tools as needed."

	systemPrompt := dreamerTaskPhaseSystemPrompt
	if useCompactTools {
		systemPrompt += "\n\n---\n## TOOLS (compact)\nTo call a tool, respond with ONLY key/value lines: TOOL: tool_name then ARGS: then one line per argument as param_name | value. No JSON, no markdown, no code fences.\n\n" + tools.GetCompactDirectoryByCategory("task")
		infra.LoggerFrom(ctx).Debug("dreamer task phase: compact tools mode", "dreamer_run_id", dreamerRunID, "phase", "task_phase")
	}

	var toolDefs []*genai.FunctionDeclaration
	if !useCompactTools {
		toolDefs = taskToolDefs
	}
	session, err := infra.NewChatSession(ctx, app, systemPrompt, toolDefs)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("dreamer task phase: failed to create session", "dreamer_run_id", dreamerRunID, "phase", "task_phase", "error", err)
		span.RecordError(err)
		return
	}

	infra.LoggerFrom(ctx).Info("dreamer task phase starting", "dreamer_run_id", dreamerRunID, "phase", "task_phase", "tool_count", len(toolDefs), "compact_tools", useCompactTools)
	var resp *genai.GenerateContentResponse
	resp, err = session.SendMessage(ctx, &genai.Part{Text: userMsg})
	if err != nil {
		infra.LoggerFrom(ctx).Warn("dreamer task phase: send failed", "dreamer_run_id", dreamerRunID, "phase", "task_phase", "error", err)
		span.RecordError(err)
		return
	}

	iteration := 1
	for iteration < DreamerTaskPhaseMaxIterations {
		if useCompactTools {
			text := infra.ExtractTextFromResponse(resp)
			infra.LoggerFrom(ctx).Debug("dreamer task phase: parsing structured tool call (K/V)", "dreamer_run_id", dreamerRunID, "raw_text", text)
			toolName, toolArgs, found := ParseStructuredToolCall(text)
			if !found {
				break
			}
			infra.ToolCallsTotal.Inc()
			toolResult := tools.Execute(ctx, app, toolName, toolArgs)
			infra.LoggerFrom(ctx).Debug("dreamer task phase tool", "dreamer_run_id", dreamerRunID, "phase", "task_phase", "tool", toolName, "success", toolResult.Success)
			resultMsg := "Tool result (" + toolName + "): " + toolResult.Result
			resp, err = session.SendMessage(ctx, &genai.Part{Text: utils.SanitizePrompt(resultMsg)})
			if err != nil {
				infra.LoggerFrom(ctx).Warn("dreamer task phase: send after tools failed", "dreamer_run_id", dreamerRunID, "phase", "task_phase", "error", err)
				span.RecordError(err)
				return
			}
			iteration++
			continue
		}
		if !infra.HasFunctionCalls(resp) {
			break
		}
		functionCalls := infra.ExtractFunctionCalls(resp)
		var parts []*genai.Part
		for _, fc := range functionCalls {
			args := make(map[string]interface{})
			for k, v := range fc.Args {
				args[k] = v
			}
			toolResult := tools.Execute(ctx, app, fc.Name, args)
			infra.LoggerFrom(ctx).Debug("dreamer task phase tool", "dreamer_run_id", dreamerRunID, "phase", "task_phase", "tool", fc.Name, "success", toolResult.Success)
			parts = append(parts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     fc.Name,
					Response: map[string]any{"result": utils.SanitizePrompt(toolResult.Result)},
				},
			})
		}
		resp, err = session.SendMessage(ctx, parts...)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer task phase: send after tools failed", "dreamer_run_id", dreamerRunID, "phase", "task_phase", "error", err)
			span.RecordError(err)
			return
		}
		iteration++
	}
	infra.LoggerFrom(ctx).Info("dreamer task phase completed", "dreamer_run_id", dreamerRunID, "phase", "task_phase", "iterations", iteration)
	span.SetAttributes(map[string]string{"iterations": fmt.Sprintf("%d", iteration)})
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
			if f == "" {
				continue
			}
			// New format: [PERSONA] or [PREFERENCE] (State-Aware Fact Blocks)
			if strings.HasPrefix(f, "[PERSONA]") || strings.HasPrefix(f, "[PREFERENCE]") {
				personaFacts = append(personaFacts, f)
				continue
			}
			// Legacy format: PERSONA: ...
			if strings.HasPrefix(f, personaPrefix) {
				personaFacts = append(personaFacts, "[PERSONA] "+strings.TrimSpace(strings.TrimPrefix(f, personaPrefix)))
			}
		}
		break
	}
	return personaFacts
}

func loadDreamerInputs(ctx context.Context, client *firestore.Client) (*DreamerInputs, error) {
	cutoff := time.Now().Add(-24 * time.Hour)
	startDate := cutoff.Format("2006-01-02")
	endDate := time.Now().Format("2006-01-02")
	entries, err := journal.GetEntriesByDateRange(ctx, client, startDate, endDate, 200)
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
	if queries, qErr := journal.GetRecentQueries(ctx, client, 50); qErr == nil && len(queries) > 0 {
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

func generateEmbedding(ctx context.Context, app *infra.App, text string, taskType ...string) ([]float32, error) {
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("app config required for embedding")
	}
	return infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, text, taskType...)
}

// RunDreamer consolidates the last 24h of journal entries into semantic memory.
// If opts is non-nil, opts.RunID is used as the run ID and opts.Progress is notified at each phase.
func RunDreamer(ctx context.Context, app *infra.App, opts *RunDreamerOpts) (*DreamerResult, error) {
	ctx, span := infra.StartSpan(ctx, "cron.dreamer")
	defer span.End()

	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}

	var dreamerRunID string
	var progress DreamerProgress
	if opts != nil {
		dreamerRunID = opts.RunID
		progress = opts.Progress
	}
	if dreamerRunID == "" {
		dreamerRunID = infra.GenShortRunID()
	}
	tDreamStart := time.Now()
	infra.LoggerFrom(ctx).Info("dreamer starting", "dreamer_run_id", dreamerRunID, "phase", "start", "window", "24h")
	if progress != nil {
		progress.OnPhase(ctx, "fetch")
	}

	client, err := app.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	inputs, err := loadDreamerInputs(ctx, client)
	if err != nil {
		span.RecordError(err)
		infra.LoggerFrom(ctx).Error("dreamer fetch entries failed", "dreamer_run_id", dreamerRunID, "phase", "fetch", "error", err)
		return nil, err
	}
	if len(inputs.EntryUUIDs) == 0 {
		infra.LoggerFrom(ctx).Info("dreamer: no entries in last 24h", "dreamer_run_id", dreamerRunID, "phase", "fetch")
		if progress != nil {
			progress.OnLog(ctx, "No entries in last 24h; nothing to consolidate.")
		}
		return &DreamerResult{EntriesProcessed: 0}, nil
	}

	infra.LoggerFrom(ctx).Info("dreamer fetched entries", "dreamer_run_id", dreamerRunID, "phase", "fetch", "count", len(inputs.EntryUUIDs), "fetch_ms", time.Since(tDreamStart).Milliseconds())
	nEntries := len(inputs.EntryUUIDs)
	if progress != nil {
		progress.OnLog(ctx, fmt.Sprintf("Fetched %d entries from last 24h.", nEntries))
		progress.OnLog(ctx, fmt.Sprintf("Processing %d entries.", nEntries))
	}
	infra.LoggerFrom(ctx).Info("dreamer journal", "dreamer_run_id", dreamerRunID, "phase", "fetch", "total_chars", len(inputs.JournalContext), "journal", inputs.JournalContext)

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

	// Pre-flight: extract active contexts before the colloquium so specialists are aware.
	if ctxs, runErr := RunContextExtractor(ctx, app, journalContext); runErr != nil {
		infra.LoggerFrom(ctx).Warn("dreamer context extractor failed (pre-colloquium)", "dreamer_run_id", dreamerRunID, "error", runErr)
	} else {
		impactedContexts = ctxs
	}

	// --- PHASE 1: THE COLLOQUIUM (Discussion Room) ---
	if progress != nil {
		progress.OnPhase(ctx, "colloquium")
	}
	tColloquiumStart := time.Now()
	roomSeed := "Room is open. Initial pass."
	if len(impactedContexts) > 0 {
		roomSeed += "\nActive project contexts: [" + strings.Join(impactedContexts, ", ") + "]."
	}
	roomTranscript := roomSeed + "\n"
	const maxRoomPasses = 10 // up to 10 passes; specialists may reply DONE early

	for pass := 1; pass <= maxRoomPasses; pass++ {
		infra.LoggerFrom(ctx).Info("dreamer colloquium pass starting", "dreamer_run_id", dreamerRunID, "phase", "colloquium", "pass", pass, "total_agents", len(domains))
		var newMessages []string
		allDone := true
		waitingCount := 0

		discussResults := make([]discussResult, len(domains))
		dg, dgctx := errgroup.WithContext(ctx)
		for i, domain := range domains {
			idx, d := i, domain
			dg.Go(func() error {
				msg, done, runErr := RunSpecialistDiscussion(dgctx, app, d, journalContext, roomTranscript, dreamerModel)
				if runErr != nil {
					infra.LoggerFrom(dgctx).Warn("specialist discussion failed", "dreamer_run_id", dreamerRunID, "phase", "colloquium", "domain", d, "error", runErr)
					discussResults[idx] = discussResult{isDone: true} // treat error as done to avoid blocking
					return nil
				}
				discussResults[idx] = discussResult{msg: msg, isDone: done}
				return nil
			})
		}
		_ = dg.Wait() // errors are soft (logged above)

		allDone = true
		waitingCount = 0
		for i, d := range domains {
			r := discussResults[i]
			if !r.isDone {
				allDone = false
				waitingCount++
				if r.msg != "" {
					newMessages = append(newMessages, fmt.Sprintf("[%s]: %s", d, r.msg))
				}
			} else {
				infra.LoggerFrom(ctx).Debug("specialist is done", "domain", d)
			}
		}

		infra.LoggerFrom(ctx).Info("dreamer colloquium pass complete", "dreamer_run_id", dreamerRunID, "phase", "colloquium", "pass", pass, "waiting_agents", waitingCount, "total_agents", len(domains))

		if len(newMessages) > 0 {
			roomTranscript += fmt.Sprintf("\n--- Pass %d ---\n%s\n", pass, strings.Join(newMessages, "\n"))
		}

		if allDone {
			infra.LoggerFrom(ctx).Info("all specialists declared DONE early", "dreamer_run_id", dreamerRunID, "phase", "colloquium", "pass", pass)
			break
		}
	}
	infra.LoggerFrom(ctx).Info("dreamer colloquium finished", "dreamer_run_id", dreamerRunID, "phase", "colloquium", "duration_ms", time.Since(tColloquiumStart).Milliseconds())
	if progress != nil {
		progress.OnLog(ctx, "Colloquium finished.")
	}

	// Attach the final transcript to the input for the final extraction phase
	input.RoomContext = roomTranscript
	infra.LoggerFrom(ctx).Info("dreamer colloquium room_transcript", "dreamer_run_id", dreamerRunID, "phase", "colloquium", "room_transcript", roomTranscript)

	// --- PHASE 2: FINAL EXTRACTION (parallel) ---
	if progress != nil {
		progress.OnPhase(ctx, "extraction")
		progress.OnLog(ctx, fmt.Sprintf("Extracting from %d domains.", len(domains)))
	}

	eg, egctx := errgroup.WithContext(ctx)

	// 2a. Five domain specialists in parallel
	for i, d := range domains {
		idx, domain := i, d
		eg.Go(func() error {
			infra.LoggerFrom(egctx).Info("dreamer final extraction start", "dreamer_run_id", dreamerRunID, "phase", "extraction", "domain", domain)
			if progress != nil {
				progress.OnLog(egctx, fmt.Sprintf("  extracting: %s", domain))
			}
			out, runErr := RunSpecialist(egctx, app, domain, input, dreamerModel)
			if runErr != nil {
				infra.LoggerFrom(egctx).Warn("dreamer specialist extraction failed", "dreamer_run_id", dreamerRunID, "phase", "extraction", "domain", domain, "error", runErr)
				return nil // soft error
			}
			outputs[idx] = out
			infra.LoggerFrom(egctx).Info("dreamer specialist extraction done", "dreamer_run_id", dreamerRunID, "phase", "extraction", "domain", domain, "facts", len(out.Facts))
			return nil
		})
	}

	// 2b. Query analyzer in parallel with specialists
	var queryAnalysisResult string
	eg.Go(func() error {
		analysis, runErr := RunQueryAnalyzer(egctx, app, recentQueriesText)
		if runErr != nil {
			infra.LoggerFrom(egctx).Warn("dreamer query analyzer failed", "dreamer_run_id", dreamerRunID, "phase", "extraction", "error", runErr)
			return nil
		}
		queryAnalysisResult = analysis
		return nil
	})

	_ = eg.Wait()
	queryAnalysis = queryAnalysisResult

	// Check if at least one specialist succeeded
	anyOk := false
	for i := range domains {
		if outputs[i] != nil {
			anyOk = true
			break
		}
	}
	if !anyOk {
		span.RecordError(fmt.Errorf("all specialists failed"))
		return nil, fmt.Errorf("dreamer: all specialists failed")
	}

	// Log per-domain fact counts now that all are complete
	for i, d := range domains {
		if outputs[i] != nil && len(outputs[i].Facts) > 0 && progress != nil {
			progress.OnLog(ctx, fmt.Sprintf("    %s: %d facts", d, len(outputs[i].Facts)))
		}
	}

	for _, name := range impactedContexts {
		ctxUUID, e := memory.EnsureContextExists(ctx, app, name)
		if e != nil {
			infra.LoggerFrom(ctx).Warn("dreamer ensure context failed", "dreamer_run_id", dreamerRunID, "phase", "consolidation", "name", name, "error", e)
			continue
		}
		contextUUIDs[ctxUUID] = struct{}{}
	}
	for uuid := range contextUUIDs {
		if e := memory.TouchContextBatch(ctx, app, uuid, entryUUIDs, 0.05); e != nil {
			infra.LoggerFrom(ctx).Warn("dreamer touch context batch failed", "dreamer_run_id", dreamerRunID, "phase", "consolidation", "context_uuid", uuid, "error", e)
		}
	}

	if queryAnalysis != "" {
		thoughtContent := "Query analysis: " + queryAnalysis
		if _, e := memory.UpsertSemanticMemory(ctx, app, thoughtContent, "thought", "selfmodel", 0.9, nil, nil); e != nil {
			infra.LoggerFrom(ctx).Warn("dreamer save query analysis thought failed", "dreamer_run_id", dreamerRunID, "phase", "consolidation", "error", e)
		} else {
			infra.LoggerFrom(ctx).Info("dreamer saved query analysis thought", "dreamer_run_id", dreamerRunID, "phase", "consolidation", "len", len(queryAnalysis))
		}
	}

	infra.LoggerFrom(ctx).Info("dreamer specialists complete", "dreamer_run_id", dreamerRunID, "phase", "extraction", "specialists_ms", time.Since(tSpecialistsStart).Milliseconds())
	if progress != nil {
		progress.OnLog(ctx, "Specialist extraction complete.")
	}

	totalFacts := 0
	for i := range domains {
		if outputs[i] != nil {
			totalFacts += len(outputs[i].Facts)
		}
	}
	infra.LoggerFrom(ctx).Info("dreamer starting consolidation", "dreamer_run_id", dreamerRunID, "phase", "consolidation", "total_facts", totalFacts)
	if progress != nil {
		progress.OnPhase(ctx, "consolidation")
	}

	merged := mergeDreamerFacts(ctx, app, domains, outputs)
	infra.LoggerFrom(ctx).Info("dreamer merge complete", "dreamer_run_id", dreamerRunID, "phase", "consolidation", "before", totalFacts, "after", len(merged), "msg", fmt.Sprintf("dreamer merged %d facts into %d", totalFacts, len(merged)))
	if progress != nil {
		progress.OnLog(ctx, fmt.Sprintf("Merged %d facts into %d unique.", totalFacts, len(merged)))
		if len(merged) > 0 {
			progress.OnLog(ctx, fmt.Sprintf("Writing %d facts to memory...", len(merged)))
		}
	}

	written, _ := dreamerWriteMergedFacts(ctx, app, merged, inputs.EntryUUIDs, progress)
	if progress != nil {
		progress.OnLog(ctx, fmt.Sprintf("Consolidation complete: %d facts written.", written))
	}

	if progress != nil {
		progress.OnPhase(ctx, "gap_detection")
		progress.OnLog(ctx, "Checking for gaps and contradictions...")
	}
	if err = RunGapDetection(ctx, app, inputs.JournalContext, inputs.EntryUUIDs); err != nil {
		infra.LoggerFrom(ctx).Warn("dreamer gap detection failed", "dreamer_run_id", dreamerRunID, "phase", "gap_detection", "error", err)
	} else {
		infra.LoggerFrom(ctx).Info("dreamer gap detection completed", "dreamer_run_id", dreamerRunID, "phase", "gap_detection")
	}

	if progress != nil {
		progress.OnPhase(ctx, "synthesis")
		progress.OnLog(ctx, "Synthesizing contexts and profile...")
	}
	synthesized, skippedLazy, _ := dreamerSynthesizeContexts(ctx, app, contextUUIDs)
	if skippedLazy > 0 {
		infra.LoggerFrom(ctx).Info("dreamer synthesis skipped (lazy)", "dreamer_run_id", dreamerRunID, "phase", "synthesis", "count", skippedLazy)
	}

	personaFacts := extractDreamerPersonaFacts(outputs, domains)
	if len(personaFacts) > 0 {
		if err = RunProfileSynthesis(ctx, app, personaFacts); err != nil {
			infra.LoggerFrom(ctx).Warn("dreamer profile synthesis failed", "dreamer_run_id", dreamerRunID, "phase", "synthesis", "error", err)
		} else {
			infra.LoggerFrom(ctx).Info("dreamer profile synthesis completed", "dreamer_run_id", dreamerRunID, "phase", "synthesis", "persona_facts", len(personaFacts))
		}
	}

	var evolutionAudit *EvolutionAuditOutput
	if audit, synErr := RunEvolutionSynthesis(ctx, app, journalContext); synErr != nil {
		infra.LoggerFrom(ctx).Warn("dreamer evolution synthesis failed", "dreamer_run_id", dreamerRunID, "phase", "synthesis", "error", synErr)
	} else {
		evolutionAudit = audit
		infra.LoggerFrom(ctx).Info("dreamer evolution synthesis completed", "dreamer_run_id", dreamerRunID, "phase", "synthesis")
	}

	// Momentum/incubation: promote recurring themes (tags/category across multiple days) to formal contexts.
	if progress != nil {
		progress.OnPhase(ctx, "incubation")
		progress.OnLog(ctx, "Promoting recurring themes to contexts...")
	}
	if promoted, incErr := memory.PromoteIncubatingClusters(ctx, app); incErr != nil {
		infra.LoggerFrom(ctx).Warn("dreamer incubation failed", "dreamer_run_id", dreamerRunID, "phase", "incubation", "error", incErr)
	} else if promoted > 0 {
		infra.LoggerFrom(ctx).Info("dreamer incubation completed", "dreamer_run_id", dreamerRunID, "phase", "incubation", "promoted", promoted)
		if progress != nil {
			progress.OnLog(ctx, fmt.Sprintf("Incubation: %d context(s) promoted.", promoted))
		}
	}

	// Generate and store the Dream Narrative (morning readout) for the user.
	if progress != nil {
		progress.OnPhase(ctx, "narrative")
	}
	dreamNarrative, narrativeErr := writeDreamNarrative(ctx, app, &dreamNarrativeInput{
		EntriesProcessed:    len(entryUUIDs),
		FactsExtracted:      totalFacts,
		FactsWritten:        written,
		ContextsSynthesized: synthesized,
		PersonaFacts:        personaFacts,
		EvolutionAudit:      evolutionAudit,
	})
	if narrativeErr != nil {
		infra.LoggerFrom(ctx).Warn("dreamer narrative failed", "dreamer_run_id", dreamerRunID, "phase", "narrative", "error", narrativeErr)
	}
	if dreamNarrative != "" && app.Config() != nil && app.Config().DebugReportEnabled {
		cfg := app.Config()
		mergedContents := make([]string, 0, len(merged))
		for _, f := range merged {
			mergedContents = append(mergedContents, f.Content)
		}
		dreamerIn := &DreamerReportInput{
			EntriesProcessed:    len(entryUUIDs),
			FactsExtracted:      totalFacts,
			FactsWritten:        written,
			ContextsSynthesized: synthesized,
			PersonaFacts:        personaFacts,
			EvolutionAudit:      evolutionAudit,
			RoomTranscript:      roomTranscript,
			DomainOutputs:       outputs,
			MergedFacts:         mergedContents,
		}
		asyncCtx := context.WithoutCancel(ctx)
		app.SubmitAsync(func() {
			processNarrative := GenerateDreamerReport(asyncCtx, app, dreamerIn)
			if processNarrative != "" {
				gdoc.WriteReport(asyncCtx, cfg, processNarrative)
			}
		})
	}

	// Let the dreamer create or update tasks from the night's journal (tool-calling phase).
	if progress != nil {
		progress.OnPhase(ctx, "task_phase")
	}
	runDreamerTaskPhase(ctx, app, dreamerRunID, journalContext, entryUUIDs)

	infra.LoggerFrom(ctx).Info("dreamer completed", "dreamer_run_id", dreamerRunID, "phase", "complete", "entries_processed", len(entryUUIDs), "facts_extracted", totalFacts, "facts_written", written, "contexts_synthesized", synthesized, "total_ms", time.Since(tDreamStart).Milliseconds(), "msg", fmt.Sprintf("dreamer completed: %d entries -> %d extracted -> %d written, %d contexts synthesized in %dms", len(entryUUIDs), totalFacts, written, synthesized, time.Since(tDreamStart).Milliseconds()))
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
