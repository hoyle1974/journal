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
	ctx, span := infra.StartSpan(ctx, "agent.dream_cycle")
	defer span.End()

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

	// ── Phase E: build prompt and call LLM ──────────────────────────────────
	now := time.Now()
	prompt, err := prompts.BuildDreamer(prompts.DreamerData{
		Today:         now.Format("2006-01-02"),
		CurrentTime:   now.Format("15:04 MST"),
		EntriesText:   utils.WrapAsUserData(dreamFormatEntries(entries)),
		OpenTasksText: utils.WrapAsUserData(dreamFormatTasks(openTasks)),
	})
	if err != nil {
		return nil, fmt.Errorf("dream cycle: build prompt: %w", err)
	}

	log.Info("dreamer: calling LLM")
	resp, err := app.Dispatch(ctx, &infra.LLMRequest{
		Parts:     []*genai.Part{{Text: prompt}},
		GenConfig: &infra.GenConfig{MaxOutputTokens: 1024},
	})
	if err != nil {
		return nil, fmt.Errorf("dream cycle: LLM call: %w", err)
	}

	raw := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
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
	span.SetAttributes(map[string]string{"dream.summary_uuid": summaryUUID})

	// ── Phase H: commit questions as pending_question nodes ───────────────────
	var savedQuestions []string
	for _, q := range questionLines {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		pq := memory.PendingQuestion{
			Question: q,
			Kind:     "gap",
			Context:  "Generated by Dreamer background cycle",
		}
		if insertErr := app.MemoryAgent().InsertPendingQuestions(ctx, []memory.PendingQuestion{pq}); insertErr != nil {
			log.Warn("dreamer: failed to insert question (continuing)", "question", q, "error", insertErr)
			continue
		}
		savedQuestions = append(savedQuestions, q)
		log.Info("dreamer: question created", "question", q)
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
