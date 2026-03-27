package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

// AddEntryAndEnqueue adds the entry to the journal and enqueues process-entry (or runs it inline if enqueue fails). Returns entry UUID.
// imageURL is optional (e.g. gs://bucket/path); pass "" when no image.
// app is passed explicitly; use app.Firestore(ctx) for journal and app for enqueue/ProcessEntry.
func AddEntryAndEnqueue(ctx context.Context, app *infra.App, content, source string, timestamp *string, imageURL string) (string, error) {
	if app == nil {
		return "", fmt.Errorf("app required for AddEntryAndEnqueue")
	}
	entryUUID, err := app.Memory.AddEntry(ctx, content, source, timestamp, imageURL)
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
		// Entry is already saved to Firestore. Do not return an error — doing so would cause
		// the user to retry and create a duplicate. The entry will remain un-embedded/un-analyzed
		// until manually re-processed or the next successful enqueue.
		infra.LoggerFrom(ctx).Error("CRITICAL: entry saved but async processing queue failed; entry will remain un-embedded", "entry_uuid", entryUUID, "task_id", taskID, "parent_trace_id", parentTraceID, "error", err)
	} else {
		infra.LoggerFrom(ctx).Debug("triggering async task", "event", "async_task_enqueued", "task", "process-entry", "task_id", taskID, "parent_trace_id", parentTraceID, "entry_uuid", entryUUID, "reason", "async processing for evaluator, analysis, embedding")
	}
	return entryUUID, nil
}

// AddEntryOnly saves the journal entry and returns its UUID without enqueueing async processing.
// Use for the unified synchronous pipeline where processing happens inline after this call.
func AddEntryOnly(ctx context.Context, app *infra.App, content, source string, timestamp *string, imageURL string) (string, error) {
	if app == nil {
		return "", fmt.Errorf("app required for AddEntryOnly")
	}
	return app.Memory.AddEntry(ctx, content, source, timestamp, imageURL)
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
		"answer":          utils.SanitizeImageSentinels(answer),
		"source":          source,
		"is_gap":          isGap,
		"task_id":         taskID,
		"parent_trace_id": parentTraceID,
	})
}
