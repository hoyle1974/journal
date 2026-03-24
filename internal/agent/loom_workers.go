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
// This replaces the commitment-detection logic that was previously inside ProcessEntry.
// Phase 4 of Project Loom will expand this with richer graph context linking.
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

// runResponseWorker generates a proactive response node using 2-hop RAG context.
// graphExtractFailed signals that Stage 2 (refinery) did not complete — the response
// should note limited context.
//
// STUB: Phase 4 (plan 2026-03-23-project-loom-phases-3-4.md Task 6) will implement
// full 2-hop retrieval, LLM call, and response node storage.
func runResponseWorker(ctx context.Context, app *infra.App, logUUID, logContent string, graphExtractFailed bool) error {
	ctx, span := infra.StartSpan(ctx, "loom.response_worker")
	defer span.End()

	if graphExtractFailed {
		infra.LoggerFrom(ctx).Info("loom response worker: stub — graph extraction failed; degraded context noted for Phase 4 implementation",
			"log_uuid", logUUID)
		return nil
	}
	// Phase 4 will: vector-search top-5 relationships, expand hot_edges, fetch pending tasks,
	// call LLM, and store a NodeTypeResponse node with logic_trace.
	infra.LoggerFrom(ctx).Info("loom response worker: stub — no response node generated yet", "log_uuid", logUUID)
	return nil
}
