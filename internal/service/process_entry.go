package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
)

// ProcessEntry runs evaluator, context detection, journal analysis, and embedding for an entry.
// Returns a latency breakdown so callers can log where time was spent (llm, embedding, firestore_write, overhead).
func ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) (*infra.LatencyBreakdown, error) {
	start := time.Now()
	var llm, embeddingDur, firestoreWrite time.Duration

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

	infra.LoggerFrom(ctx).Debug("process-entry: running evaluator", "entry_uuid", entryUUID, "reason", "extract significance and optionally store fact")
	t0 := time.Now()
	agent.RunEvaluator(ctx, ServiceEnv{}, content, entryUUID, timestamp)
	llm += time.Since(t0)

	t1 := time.Now()
	contextUUIDs, err := memory.DetectOrCreateContext(ctx, content, entryUUID)
	firestoreWrite += time.Since(t1)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("context detection failed", "error", err)
	}
	contextCount := len(contextUUIDs)
	infra.LoggerFrom(ctx).Debug("process-entry: context detection done", "entry_uuid", entryUUID, "contexts_linked", contextCount, "reason", "link entry to active contexts")

	t2 := time.Now()
	analysis, err := journal.AnalyzeJournalEntry(ctx, content, entryUUID, timestamp)
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

	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		breakdown := buildBreakdown(start, llm, embeddingDur, firestoreWrite)
		return breakdown, fmt.Errorf("no app config for embedding")
	}
	t3 := time.Now()
	vector, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, content, infra.EmbedTaskRetrievalDocument)
	embeddingDur = time.Since(t3)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("failed to generate entry embedding", "entry_uuid", entryUUID, "error", err)
		breakdown := buildBreakdown(start, llm, embeddingDur, firestoreWrite)
		return breakdown, fmt.Errorf("embedding: %w", err)
	}
	infra.LoggerFrom(ctx).Debug("process-entry embedding generated", "entry_uuid", entryUUID, "dimensions", len(vector), "reason", "for semantic search")

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("failed to get firestore for entry embedding", "error", err)
		breakdown := buildBreakdown(start, llm, embeddingDur, firestoreWrite)
		return breakdown, err
	}
	updates := []firestore.Update{{Path: "embedding", Value: firestore.Vector32(vector)}}
	if analysisJSON != "" {
		updates = append(updates, firestore.Update{Path: "journal_analysis", Value: analysisJSON})
	}
	infra.LoggerFrom(ctx).Debug("process-entry: writing embedding and analysis to Firestore", "entry_uuid", entryUUID, "reason", "persist for RAG and rollups")
	t4 := time.Now()
	_, err = client.Collection(journal.EntriesCollection).Doc(entryUUID).Update(ctx, updates)
	firestoreWrite += time.Since(t4)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("failed to store entry embedding", "entry_uuid", entryUUID, "error", err)
		breakdown := buildBreakdown(start, llm, embeddingDur, firestoreWrite)
		return breakdown, err
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
	return breakdown, nil
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
