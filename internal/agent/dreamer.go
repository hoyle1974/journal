package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/system"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/genai"
)

const (
	// dreamMinEntries is the minimum number of new log entries required to run a
	// dream cycle without the force flag.
	dreamMinEntries = 5
	// dreamMinInterval is the minimum time since the last cycle before it will run
	// again without the force flag.
	dreamMinInterval = 24 * time.Hour
	// dreamMaxEntries caps log entries passed to the LLM to bound token use.
	dreamMaxEntries = 50
	// dreamSummaryWeight is the significance_weight on summary nodes.
	dreamSummaryWeight = 0.8
)

// DreamResult summarises the outcome of a single dream cycle.
type DreamResult struct {
	SummaryUUID string
	Questions   []string
	Skipped     bool
	SkipReason  string
}

// RunDreamCycle synthesises recent log entries into a summary node and enqueues
// follow-up questions as pending_question nodes.
//
// When force is false the cycle is skipped if fewer than dreamMinEntries new
// entries exist AND fewer than dreamMinInterval have elapsed since the last run.
func RunDreamCycle(ctx context.Context, app *infra.App, force bool) (*DreamResult, error) {
	log := infra.LoggerFrom(ctx)
	log.Info("dreamer: cycle started", "force", force)

	// ── Phase A: read watermark ──────────────────────────────────────────────
	meta, err := system.GetDreamMeta(ctx, app)
	if err != nil {
		return nil, fmt.Errorf("dream cycle: read meta: %w", err)
	}
	log.Info("dreamer: watermark read", "last_processed_at", meta.LastProcessedAt)

	// ── Phase B: fetch log entries since watermark ───────────────────────────
	entries, err := dreamFetchEntries(ctx, app, meta.LastProcessedAt)
	if err != nil {
		return nil, fmt.Errorf("dream cycle: fetch entries: %w", err)
	}
	log.Info("dreamer: entries fetched", "count", len(entries))

	// ── Phase C: threshold check ─────────────────────────────────────────────
	if !force {
		skip, reason, skipErr := dreamShouldSkip(meta, len(entries))
		if skipErr != nil {
			log.Warn("dreamer: watermark parse error — proceeding anyway", "error", skipErr, "last_processed_at", meta.LastProcessedAt)
		} else if skip {
			log.Info("dreamer: skipping dream cycle", "reason", reason)
			return &DreamResult{Skipped: true, SkipReason: reason}, nil
		}
	}
	log.Info("dreamer: threshold check passed", "entry_count", len(entries))

	// Cap entries passed to the LLM.
	if len(entries) > dreamMaxEntries {
		entries = entries[len(entries)-dreamMaxEntries:]
	}

	// ── Phase D: fetch open tasks ────────────────────────────────────────────
	openTasks, err := app.MemoryTasks().GetOpenRootTasks(ctx, 10)
	if err != nil {
		log.Warn("dreamer: could not fetch open tasks (continuing without)", "error", err)
	}

	// ── Phase D.1: fetch recent questions (open + answered) ──────────────────
	openQuestions, err := app.MemoryAgent().GetUnresolvedQuestions(ctx, 20)
	if err != nil {
		log.Warn("dreamer: could not fetch open questions (continuing without)", "error", err)
	}
	resolvedQuestions, err := app.MemoryAgent().GetRecentlyResolvedQuestions(ctx, time.Now().AddDate(0, 0, -30))
	if err != nil {
		log.Warn("dreamer: could not fetch resolved questions (continuing without)", "error", err)
	}

	// ── Phase D.5: Build RAG Context ─────────────────────────────────────────
	var combinedContent strings.Builder
	for _, e := range entries {
		combinedContent.WriteString(e.Content)
		combinedContent.WriteString("\n")
	}

	var ragContext string
	if combinedContent.Len() > 0 {
		searchContent := utils.TruncateString(combinedContent.String(), 4000)
		ragCtx, err := BuildLoomRAGContext(ctx, app, "", searchContent, nil)
		if err != nil {
			log.Warn("dreamer: failed to build RAG context (continuing without)", "error", err)
		} else if ragCtx != nil {
			ragContext = ragCtx.FormatForPrompt()
		}
	}

	// ── Phase E: build prompt and call LLM ──────────────────────────────────
	now := time.Now()
	prompt, err := prompts.BuildDreamer(prompts.DreamerData{
		Today:               now.Format("2006-01-02"),
		CurrentTime:         now.Format("15:04 MST"),
		EntriesText:         utils.WrapAsUserData(dreamFormatEntries(entries)),
		OpenTasksText:       utils.WrapAsUserData(dreamFormatTasks(openTasks)),
		LoomContextBlock:    utils.WrapAsUserData(ragContext),
		RecentQuestionsText: utils.WrapAsUserData(dreamFormatQuestions(openQuestions, resolvedQuestions)),
	})
	if err != nil {
		return nil, fmt.Errorf("dream cycle: build prompt: %w", err)
	}

	log.Info("dreamer: calling LLM with native CoT")

	session, err := infra.NewChatSession(ctx, app, "", nil, true)
	if err != nil {
		return nil, fmt.Errorf("dream cycle: create chat session: %w", err)
	}

	resp, err := session.SendMessage(ctx, &genai.Part{Text: prompt})
	if err != nil {
		return nil, fmt.Errorf("dream cycle: LLM call: %w", err)
	}

	thinking, raw := infra.ExtractThinkingAndAnswer(resp)
	if thinking != "" {
		log.Debug("dreamer: CoT trace", "thinking", thinking)
	}

	raw = strings.TrimSpace(raw)
	log.Debug("dreamer: LLM response received", "response", raw)

	// ── Phase F: parse LLM output ────────────────────────────────────────────
	simple, sections := utils.ParseKeyValueMap(raw)
	summaryText := strings.TrimSpace(simple["summary"])
	if summaryText == "" {
		return nil, fmt.Errorf("dream cycle: LLM returned no summary line")
	}

	questionLines := sections["questions"]
	log.Info("dreamer: parsed LLM output",
		"summary_len", len(summaryText),
		"question_count", len(questionLines))

	// ── Phase G: commit summary node ─────────────────────────────────────────
	summaryUUID, err := app.MemoryKnowledge().Upsert(ctx,
		summaryText,
		memory.NodeTypeSummary,
		"dreamer",
		dreamSummaryWeight,
		memory.UpsertOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("dream cycle: commit summary: %w", err)
	}
	log.Info("dreamer: summary committed", "uuid", summaryUUID)

	// ── Phase G.5: parse question lines and self-check ───────────────────────
	type parsedQuestion struct {
		question string
		reason   string
	}
	var filteredQuestions []parsedQuestion
	for _, line := range questionLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var pq parsedQuestion
		if q, reason, ok := strings.Cut(line, " | reason: "); ok {
			pq.question = strings.TrimSpace(q)
			pq.reason = strings.TrimSpace(reason)
		} else {
			pq.question = line
		}
		answerable, checkErr := dreamSelfCheckQuestion(ctx, app, pq.question)
		if checkErr != nil {
			log.Warn("dreamer: self-check error (keeping question)", "question", pq.question, "error", checkErr)
			filteredQuestions = append(filteredQuestions, pq)
			continue
		}
		if answerable {
			log.Info("dreamer: dropped self-answerable question", "question", pq.question)
			continue
		}
		filteredQuestions = append(filteredQuestions, pq)
	}
	log.Info("dreamer: self-check complete",
		"candidates", len(questionLines),
		"kept", len(filteredQuestions))

	// ── Phase H: commit questions as pending_question nodes ───────────────────
	var savedQuestions []string
	for _, pq := range filteredQuestions {
		if pq.question == "" {
			continue
		}
		pendingQ := memory.PendingQuestion{
			Question: pq.question,
			Kind:     "gap",
			Context:  pq.reason,
		}
		if insertErr := app.MemoryAgent().InsertPendingQuestions(ctx, []memory.PendingQuestion{pendingQ}); insertErr != nil {
			log.Warn("dreamer: failed to insert question (continuing)", "question", pq.question, "error", insertErr)
			continue
		}
		savedQuestions = append(savedQuestions, pq.question)
		log.Info("dreamer: question created", "question", pq.question)
	}

	// ── Phase I: update watermark ────────────────────────────────────────────
	newMeta := system.DreamMeta{
		LastProcessedAt: now.Format(time.RFC3339),
		Version:         meta.Version,
	}
	if setErr := system.SetDreamMeta(ctx, app, newMeta); setErr != nil {
		// Non-fatal: double-processing is harmless — questions get deduped by embeddings.
		log.Warn("dreamer: failed to update watermark (non-fatal)", "error", setErr)
	}
	log.Info("dreamer: cycle complete",
		"summary_uuid", summaryUUID,
		"questions_saved", len(savedQuestions))

	return &DreamResult{
		SummaryUUID: summaryUUID,
		Questions:   savedQuestions,
	}, nil
}

// GetRecentSummaries fetches the n most recent summary nodes for FOH prompt injection.
func GetRecentSummaries(ctx context.Context, env infra.ToolEnv, limit int) ([]string, error) {
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("get recent summaries: firestore: %w", err)
	}
	docs, err := client.Collection(memory.KnowledgeCollection).
		Where("node_type", "==", memory.NodeTypeSummary).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("get recent summaries: query: %w", err)
	}
	summaries := make([]string, 0, len(docs))
	for _, doc := range docs {
		data := doc.Data()
		content, _ := data["content"].(string)
		if content == "" {
			continue
		}
		ts, _ := data["timestamp"].(string)
		if len(ts) >= 10 {
			summaries = append(summaries, fmt.Sprintf("[%s]: %s", ts[:10], content))
		} else {
			summaries = append(summaries, content)
		}
	}
	return summaries, nil
}

// dreamShouldSkip returns (skip, reason, err).
// err is non-nil when LastProcessedAt is set but unparseable; caller should warn and proceed.
func dreamShouldSkip(meta system.DreamMeta, entryCount int) (bool, string, error) {
	if entryCount >= dreamMinEntries {
		return false, "", nil
	}
	if meta.LastProcessedAt == "" {
		return false, "", nil // first run — always proceed
	}
	last, err := time.Parse(time.RFC3339, meta.LastProcessedAt)
	if err != nil {
		return false, "", fmt.Errorf("parse last_processed_at %q: %w", meta.LastProcessedAt, err)
	}
	if time.Since(last) >= dreamMinInterval {
		return false, "", nil // interval elapsed — proceed regardless of entry count
	}
	return true, fmt.Sprintf("only %d new entries (need %d) and last run was < %v ago",
		entryCount, dreamMinEntries, dreamMinInterval), nil
}

// dreamFetchEntries returns log entries with timestamp > sinceTimestamp, oldest first,
// capped at dreamMaxEntries. When sinceTimestamp is empty, returns the most recent entries.
func dreamFetchEntries(ctx context.Context, app *infra.App, sinceTimestamp string) ([]memory.Entry, error) {
	if sinceTimestamp == "" {
		// First run: use the most recent entries as a seed.
		return app.MemoryEntries().List(ctx, dreamMaxEntries, false)
	}
	client, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch entries since: firestore: %w", err)
	}
	docs, err := client.Collection(memory.KnowledgeCollection).
		Where("node_type", "==", memory.NodeTypeLog).
		Where("timestamp", ">", sinceTimestamp).
		OrderBy("timestamp", firestore.Asc).
		Limit(dreamMaxEntries).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("fetch entries since: query: %w", err)
	}
	entries := make([]memory.Entry, 0, len(docs))
	for _, doc := range docs {
		var e memory.Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}
		e.UUID = doc.Ref.ID
		entries = append(entries, e)
	}
	return entries, nil
}

// dreamFormatEntries renders log entries as a numbered list for the LLM prompt.
func dreamFormatEntries(entries []memory.Entry) string {
	if len(entries) == 0 {
		return "(no recent entries)"
	}
	var sb strings.Builder
	for i, e := range entries {
		ts := e.Timestamp
		if len(ts) > 10 {
			ts = ts[:10]
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, ts, e.Content))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// dreamFormatQuestions renders recently asked (open) and answered questions for the LLM prompt.
// Open questions are listed first so the model knows what's still pending; answered questions
// show question + answer so the model doesn't surface the same gap again.
func dreamFormatQuestions(open, resolved []memory.PendingQuestion) string {
	if len(open) == 0 && len(resolved) == 0 {
		return "(none)"
	}
	var sb strings.Builder
	for _, q := range open {
		sb.WriteString(fmt.Sprintf("[OPEN] %s\n", q.Question))
	}
	for _, q := range resolved {
		ans := strings.TrimSpace(q.Answer)
		if ans == "" {
			ans = "(skipped)"
		}
		sb.WriteString(fmt.Sprintf("[ANSWERED] %s → %s\n", q.Question, ans))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// dreamFormatTasks renders open root tasks as a numbered list for the LLM prompt.
func dreamFormatTasks(tasks []memory.Task) string {
	if len(tasks) == 0 {
		return "(no open tasks)"
	}
	var sb strings.Builder
	for i, t := range tasks {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, t.Status, t.Content))
	}
	return strings.TrimRight(sb.String(), "\n")
}
