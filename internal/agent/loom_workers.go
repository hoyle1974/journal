package agent

import (
	"context"
	"strings"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
)

// runTaskWorker scans logContent for commitment signals and auto-creates Task nodes
// after graph objects exist so the task can link to them.
// extractedObjectIDs are the UUIDs of object/log nodes associated with this pipeline run.
//
// This replaces the commitment-detection logic previously inside ProcessEntry.
func runTaskWorker(ctx context.Context, app *infra.App, logContent string, extractedObjectIDs []string) error {
	ctx, span := infra.StartSpan(ctx, "loom.task_worker")
	defer span.End()

	parsed, err := RunEvaluatorExtract(ctx, app, logContent)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("loom task worker: evaluator extract failed", "error", err)
		return err
	}
	if parsed == nil {
		return nil
	}
	if parsed.FutureCommitment < AgencyTaskCommitmentThreshold {
		return nil
	}
	intent := strings.TrimSpace(parsed.CommitmentIntent)
	if len(intent) < MinCommitmentIntentLen {
		return nil
	}
	t := &memory.Task{
		Content:         intent,
		Status:          memory.TaskStatusPending,
		JournalEntryIDs: extractedObjectIDs,
	}
	taskUUID, err := app.Memory.CreateTask(ctx, t)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("loom task worker: create task failed", "error", err)
		return err
	}
	infra.LoggerFrom(ctx).Info("loom task worker: task created", "task_uuid", taskUUID, "intent", intent)
	return nil
}

