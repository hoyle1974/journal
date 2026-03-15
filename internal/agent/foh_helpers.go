package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/journal"
)

// AddEntryAndEnqueue adds the entry to the journal and enqueues process-entry (or runs it inline if enqueue fails). Returns entry UUID.
// app is passed explicitly; use app.Firestore(ctx) for journal and app for enqueue/ProcessEntry.
func AddEntryAndEnqueue(ctx context.Context, app *infra.App, content, source string, timestamp *string) (string, error) {
	if app == nil {
		return "", fmt.Errorf("app required for AddEntryAndEnqueue")
	}
	client, err := app.Firestore(ctx)
	if err != nil {
		return "", err
	}
	entryUUID, err := journal.AddEntry(ctx, client, content, source, timestamp)
	if err != nil {
		return "", err
	}
	ts := time.Now().Format(time.RFC3339)
	if timestamp != nil && *timestamp != "" {
		ts = *timestamp
	}
	taskID := "process-entry-" + uuid.New().String()[:8]
	parentTraceID := infra.TraceIDFromContext(ctx)
	payload := map[string]interface{}{
		"uuid": entryUUID, "content": content, "timestamp": ts, "source": source,
		"task_id": taskID, "parent_trace_id": parentTraceID,
	}
	if err := app.EnqueueTask(ctx, "/internal/process-entry", payload); err != nil {
		infra.LoggerFrom(ctx).Debug("process-entry enqueue failed, running inline", "entry_uuid", entryUUID, "task_id", taskID, "parent_trace_id", parentTraceID, "reason", "Cloud Tasks unavailable; processing in background goroutine")
		infra.LoggerFrom(ctx).Warn("failed to enqueue process-entry task, running inline", "entry_uuid", entryUUID, "task_id", taskID, "parent_trace_id", parentTraceID, "error", err)
		app.SubmitAsync(func() {
			bgCtx := infra.WithCorrelation(context.Background(), taskID, parentTraceID)
			_, _ = ProcessEntry(bgCtx, app, entryUUID, content, ts, source)
		})
	} else {
		infra.LoggerFrom(ctx).Debug("triggering async task", "event", "async_task_enqueued", "task", "process-entry", "task_id", taskID, "parent_trace_id", parentTraceID, "entry_uuid", entryUUID, "reason", "async processing for evaluator, context links, analysis, embedding")
	}
	return entryUUID, nil
}

// EnqueueSaveQuery enqueues a task to save the query and answer (and whether it was a knowledge gap).
// app is passed explicitly by the caller (e.g. FOH loop).
func EnqueueSaveQuery(ctx context.Context, app *infra.App, question, answer, source string, isGap bool) error {
	if app == nil {
		return nil
	}
	taskID := "save-query-" + uuid.New().String()[:8]
	parentTraceID := infra.TraceIDFromContext(ctx)
	return app.EnqueueTask(ctx, "/internal/save-query", map[string]interface{}{
		"question":        question,
		"answer":          answer,
		"source":          source,
		"is_gap":          isGap,
		"task_id":         taskID,
		"parent_trace_id": parentTraceID,
	})
}
