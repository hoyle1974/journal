package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProcessEntryReport holds structured results from a ProcessEntry run for debug reporting.
type ProcessEntryReport struct {
	Content        string
	Source         string
	Significance   float64
	Domain         string
	FactStored     string
	TaskCreated    string // commitment intent if an agency task was auto-created; empty if none
	ContextsLinked int
	Mood           string
	Tags           []string
	EntityNames    []string
}

// ProcessEntry runs evaluator, context detection, journal analysis, and embedding for an entry.
// Returns a latency breakdown so callers can log where time was spent (llm, embedding, firestore_write, overhead).
func ProcessEntry(ctx context.Context, app *infra.App, entryUUID, content, timestamp, source string) (*infra.LatencyBreakdown, *ProcessEntryReport, error) {
	start := time.Now()
	var llm, embeddingDur, firestoreWrite time.Duration

	if app == nil || app.Config() == nil {
		breakdown := buildBreakdown(start, llm, embeddingDur, firestoreWrite)
		return breakdown, nil, fmt.Errorf("no app config for process entry")
	}

	startAttrs := []any{"event", "process_entry_start", "entry_uuid", entryUUID, "content", content, "timestamp", timestamp, "source", source}
	if corr := infra.CorrelationFromContext(ctx); corr != nil {
		if corr.TaskID != "" {
			startAttrs = append(startAttrs, "task_id", corr.TaskID)
		}
		if corr.ParentTraceID != "" {
			startAttrs = append(startAttrs, "parent_trace_id", corr.ParentTraceID)
		}
	}
	infra.LoggerFrom(ctx).Info("process-entry start", startAttrs...)

	// Log when this entry is linked to an image (e.g. Telegram photo); helps debug caption vs placeholder "Photo".
	if client, err := app.Firestore(ctx); err == nil {
		if doc, err := client.Collection(memory.EntriesCollection).Doc(entryUUID).Get(ctx); err == nil {
			if imageID := infra.GetStringField(doc.Data(), "image_file_id"); imageID != "" {
				infra.LoggerFrom(ctx).Info("process-entry: entry has linked image", "entry_uuid", entryUUID, "image_file_id", imageID)
			}
		}
	}

	infra.LoggerFrom(ctx).Debug("process-entry: running evaluator", "entry_uuid", entryUUID, "reason", "extract significance and optionally store fact")
	t0 := time.Now()
	parsed, err := RunEvaluator(ctx, app, content, entryUUID, timestamp)
	llm += time.Since(t0)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("process-entry: evaluator failed", "entry_uuid", entryUUID, "error", err)
	}
	// Agency threshold: auto-create a task when the entry expresses a high future commitment.
	var taskContent string
	if parsed != nil && parsed.FutureCommitment >= AgencyTaskCommitmentThreshold && len(strings.TrimSpace(parsed.CommitmentIntent)) >= MinCommitmentIntentLen {
		taskContent = strings.TrimSpace(parsed.CommitmentIntent)
		t := &memory.Task{
			Content:         taskContent,
			Status:          memory.TaskStatusPending,
			JournalEntryIDs: []string{entryUUID},
		}
		if taskUUID, createErr := memory.CreateTask(ctx, app, t); createErr != nil {
			infra.LoggerFrom(ctx).Warn("process-entry: agency task create failed", "entry_uuid", entryUUID, "error", createErr)
		} else {
			infra.LoggerFrom(ctx).Info("process-entry: agency task created", "entry_uuid", entryUUID, "task_uuid", taskUUID, "content", taskContent)
		}
	}

	t1 := time.Now()
	contextUUIDs, err := memory.DetectOrCreateContext(ctx, app, content, entryUUID)
	firestoreWrite += time.Since(t1)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("context detection failed", "error", err)
	}
	contextCount := len(contextUUIDs)
	infra.LoggerFrom(ctx).Debug("process-entry: context detection done", "entry_uuid", entryUUID, "contexts_linked", contextCount, "reason", "link entry to active contexts")

	t2 := time.Now()
	analysis, err := memory.AnalyzeJournalEntry(ctx, app, content, entryUUID, timestamp)
	llm += time.Since(t2)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("journal analysis failed", "entry_uuid", entryUUID, "error", err)
	}
	var analysisJSON string
	if analysis != nil {
		if b, err := json.Marshal(analysis); err == nil {
			analysisJSON = string(b)
		}
	}
	infra.LoggerFrom(ctx).Debug("process-entry: journal analysis done", "entry_uuid", entryUUID, "has_analysis", analysis != nil, "reason", "mood/tags/entities for rollup and search")
	if analysis != nil && len(analysis.Entities) > 0 {
		// Synchronous entity resolution with internal timeout. Resolves entity mentions to
		// existing knowledge nodes and links this entry to them.
		ResolveAndLinkEntities(ctx, app, entryUUID, analysis.Entities)
	}
	// Best-effort SPO relationship extraction — runs in background because it makes an LLM call.
	go func() {
		bgCtx := context.Background()
		ExtractAndStoreRelationships(bgCtx, app, entryUUID, content)
	}()

	t3 := time.Now()
	vector, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, content, infra.EmbedTaskRetrievalDocument)
	embeddingDur = time.Since(t3)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("failed to generate entry embedding", "entry_uuid", entryUUID, "error", err)
		breakdown := buildBreakdown(start, llm, embeddingDur, firestoreWrite)
		return breakdown, nil, fmt.Errorf("embedding: %w", err)
	}
	infra.LoggerFrom(ctx).Debug("process-entry embedding generated", "entry_uuid", entryUUID, "dimensions", len(vector), "reason", "for semantic search")

	client, err := app.Firestore(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("failed to get firestore for entry embedding", "error", err)
		breakdown := buildBreakdown(start, llm, embeddingDur, firestoreWrite)
		return breakdown, nil, err
	}
	updates := []firestore.Update{
		{Path: "embedding", Value: firestore.Vector32(vector)},
		{Path: "node_type", Value: "log"},
		{Path: "significance_weight", Value: 0.3},
	}
	if analysisJSON != "" {
		updates = append(updates, firestore.Update{Path: "journal_analysis", Value: analysisJSON})
	}
	infra.LoggerFrom(ctx).Debug("process-entry: writing embedding and analysis to Firestore", "entry_uuid", entryUUID, "reason", "persist for RAG and rollups")
	t4 := time.Now()
	err = updateEntryWithRetry(ctx, client, entryUUID, content, timestamp, source, updates)
	firestoreWrite += time.Since(t4)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("failed to store entry embedding", "entry_uuid", entryUUID, "error", err)
		breakdown := buildBreakdown(start, llm, embeddingDur, firestoreWrite)
		return breakdown, nil, err
	}
	total := time.Since(start)
	breakdown := buildBreakdown(start, llm, embeddingDur, firestoreWrite)
	doneAttrs := []any{"event", "process_entry_done", "entry_uuid", entryUUID, "contexts_linked", contextCount, "embedding_dims", len(vector), "has_analysis", analysisJSON != "", "duration", total}
	doneAttrs = append(doneAttrs, breakdown.LogAttrs()...)
	if corr := infra.CorrelationFromContext(ctx); corr != nil {
		if corr.TaskID != "" {
			doneAttrs = append(doneAttrs, "task_id", corr.TaskID)
		}
		if corr.ParentTraceID != "" {
			doneAttrs = append(doneAttrs, "parent_trace_id", corr.ParentTraceID)
		}
	}
	infra.LoggerFrom(ctx).Info("process-entry done", doneAttrs...)
	infra.LoggerFrom(ctx).Debug("process-entry: done", "entry_uuid", entryUUID, "reason", "evaluator, context links, analysis, and embedding all completed")
	report := &ProcessEntryReport{
		Content: utils.TruncateString(content, 500),
		Source:  source,
	}
	if parsed != nil {
		report.Significance = parsed.Significance
		report.Domain = parsed.Domain
		report.FactStored = parsed.FactToStore
	}
	if taskContent != "" {
		report.TaskCreated = taskContent
	}
	report.ContextsLinked = contextCount
	if analysis != nil {
		report.Mood = analysis.Mood
		report.Tags = analysis.Tags
		report.EntityNames = make([]string, 0, len(analysis.Entities))
		for _, e := range analysis.Entities {
			report.EntityNames = append(report.EntityNames, e.Name)
		}
	}
	return breakdown, report, nil
}

// updateEntryWithRetry runs Update on the entry doc, retrying on NotFound with backoff. If the doc is still
// missing after retries (e.g. entry was never created or create didn't propagate), creates the entry in one
// Merge Set with base fields and update fields so process-entry does not fail and we avoid a second write.
func updateEntryWithRetry(ctx context.Context, client *firestore.Client, entryUUID, content, timestamp, source string, updates []firestore.Update) error {
	const maxAttempts = 6
	backoff := []time.Duration{
		200 * time.Millisecond, // give AddEntry write time to propagate before first attempt
		400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond,
		3200 * time.Millisecond, 3200 * time.Millisecond,
	}
	ref := client.Collection(memory.EntriesCollection).Doc(entryUUID)
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 && backoff[attempt] > 0 {
			infra.LoggerFrom(ctx).Debug("process-entry: retrying entry update after NotFound", "entry_uuid", entryUUID, "attempt", attempt+1, "max_attempts", maxAttempts, "backoff_ms", backoff[attempt].Milliseconds())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff[attempt]):
			}
		} else if attempt == 0 && backoff[0] > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff[0]):
			}
		}
		_, lastErr = ref.Update(ctx, updates)
		if lastErr == nil {
			return nil
		}
		if status.Code(lastErr) != codes.NotFound {
			return lastErr
		}
	}
	// Document still missing after retries: create in one Merge Set (base + embedding/analysis) to avoid race.
	if status.Code(lastErr) == codes.NotFound {
		infra.LoggerFrom(ctx).Warn("process-entry: entry doc missing after retries, creating from payload", "entry_uuid", entryUUID, "reason", "entry may not have been written before task ran")
		merge := map[string]interface{}{
			"content":             content,
			"source":              source,
			"timestamp":           timestamp,
			"node_type":           "log",
			"significance_weight": 0.3,
		}
		for _, u := range updates {
			merge[u.Path] = u.Value
		}
		_, createErr := ref.Set(ctx, merge, firestore.MergeAll)
		if createErr != nil {
			return fmt.Errorf("create entry after NotFound: %w", createErr)
		}
		return nil
	}
	return lastErr
}

func buildBreakdown(start time.Time, llm, embedding, firestoreWrite time.Duration) *infra.LatencyBreakdown {
	total := time.Since(start)
	overhead := total - llm - embedding - firestoreWrite
	if overhead < 0 {
		overhead = 0
	}
	return &infra.LatencyBreakdown{
		LLM:            llm,
		Embedding:      embedding,
		FirestoreWrite: firestoreWrite,
		Overhead:       overhead,
	}
}
