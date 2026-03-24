package agent

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

// ProcessEntryReport holds structured results from a ProcessLogSequential run.
type ProcessEntryReport struct {
	Content          string
	Source           string
	TaskCreated      string   // commitment intent if an agency task was auto-created; empty if none
	ExtractedNodeIDs []string // subject, object, relationship UUIDs from refinery stage 2
}

// ProcessLogSequential runs the Project Loom waterfall pipeline for a new log entry.
// It executes three stages in strict sequential order before returning.
//
//	Stage 1: Log Persistence — write the raw log node to Firestore immediately.
//	Stage 2: Refinery       — extract KG triples and commit graph objects/relationships.
//	Stage 3: Task Worker    — scan for commitments and create task nodes linked to graph objects.
//
// Stage 2 and 3 failures are logged but do NOT abort the pipeline.
// Stage 4 removed — FOH+thinking replaces response worker.
func ProcessLogSequential(ctx context.Context, app *infra.App, logUUID, logContent, timestamp, source string) (*ProcessEntryReport, error) {
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("ProcessLogSequential: app or config is nil")
	}

	ctx, span := infra.StartSpan(ctx, "loom.process_log_sequential")
	defer span.End()
	span.SetAttributes(map[string]string{
		"log_uuid": logUUID,
		"source":   source,
	})

	infra.LoggerFrom(ctx).Info("loom pipeline start",
		"event", "loom_start",
		"log_uuid", logUUID,
		"source", source,
	)

	// ── Stage 1: Log Persistence ──────────────────────────────────────────────
	// Write the raw log node before any worker runs. This guarantees the document
	// exists, eliminating the retry-backoff race in the legacy updateEntryWithRetry path.
	infra.LoggerFrom(ctx).Debug("loom stage 1: log persistence", "log_uuid", logUUID)
	fsClient, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("loom stage 1: firestore client: %w", err)
	}
	logDoc := map[string]any{
		"content":             logContent,
		"source":              source,
		"timestamp":           timestamp,
		"node_type":           "log",
		"significance_weight": 0.3,
	}
	if _, setErr := fsClient.Collection(memory.EntriesCollection).Doc(logUUID).Set(ctx, logDoc, firestore.MergeAll); setErr != nil {
		return nil, fmt.Errorf("loom stage 1: write log node: %w", setErr)
	}
	infra.LoggerFrom(ctx).Info("loom stage 1 done: log node persisted", "log_uuid", logUUID)

	// ── Stage 2: Refinery ─────────────────────────────────────────────────────
	infra.LoggerFrom(ctx).Debug("loom stage 2: refinery", "log_uuid", logUUID)
	var extractedNodeIDs []string
	nodeIDs, refineryErr := runRefineryPipeline(ctx, app, logUUID, logContent)
	if refineryErr != nil {
		infra.LoggerFrom(ctx).Warn("loom stage 2 FAILED: refinery pipeline error — pipeline continues",
			"log_uuid", logUUID, "error", refineryErr)
	} else {
		extractedNodeIDs = nodeIDs
		infra.LoggerFrom(ctx).Info("loom stage 2 done: refinery complete",
			"log_uuid", logUUID, "node_count", len(extractedNodeIDs))
	}

	// ── Stage 3: Task Worker ──────────────────────────────────────────────────
	// Pass the log UUID as the initial object ID set so tasks backlink to this entry.
	infra.LoggerFrom(ctx).Debug("loom stage 3: task worker", "log_uuid", logUUID)
	taskErr := runTaskWorker(ctx, app, logContent, []string{logUUID})
	if taskErr != nil {
		infra.LoggerFrom(ctx).Warn("loom stage 3 FAILED: task worker error",
			"log_uuid", logUUID,
			"error", taskErr,
		)
	} else {
		infra.LoggerFrom(ctx).Info("loom stage 3 done: task worker complete", "log_uuid", logUUID)
	}

	// Stage 4 removed — FOH+thinking replaces response worker

	infra.LoggerFrom(ctx).Info("loom pipeline complete",
		"event", "loom_done",
		"log_uuid", logUUID,
		"stage2_ok", refineryErr == nil,
		"stage3_ok", taskErr == nil,
	)

	return &ProcessEntryReport{
		Content:          utils.TruncateString(logContent, 500),
		Source:           source,
		ExtractedNodeIDs: extractedNodeIDs,
	}, nil
}

// ProcessEntrySyncPipeline runs refinery (stage 2) and task worker (stage 3) for an entry
// that has already been persisted by the caller. Returns the node IDs extracted by the refinery.
// Use from the unified synchronous pipeline (ProcessAndRespond); use ProcessLogSequential for
// the async Cloud Task path.
func ProcessEntrySyncPipeline(ctx context.Context, app *infra.App, logUUID, logContent, source string) ([]string, error) {
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("ProcessEntrySyncPipeline: app or config is nil")
	}
	ctx, span := infra.StartSpan(ctx, "loom.process_entry_sync")
	defer span.End()
	span.SetAttributes(map[string]string{"log_uuid": logUUID, "source": source})

	nodeIDs, refineryErr := runRefineryPipeline(ctx, app, logUUID, logContent)
	if refineryErr != nil {
		infra.LoggerFrom(ctx).Warn("sync pipeline: refinery failed", "log_uuid", logUUID, "error", refineryErr)
	}
	if taskErr := runTaskWorker(ctx, app, logContent, []string{logUUID}); taskErr != nil {
		infra.LoggerFrom(ctx).Warn("sync pipeline: task worker failed", "log_uuid", logUUID, "error", taskErr)
	}
	return nodeIDs, refineryErr
}
