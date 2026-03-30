package agent

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/memory"
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
//	Stage 3: Task Worker    — scan for commitments and queue a PendingQuestion proposal for user confirmation.
//
// Stage 2 (Refinery) failures abort the pipeline and return an error so the
// Cloud Tasks handler returns HTTP 500 and retries automatically. Stage 3
// (Task Worker) failures are logged but do not abort (retry idempotency not
// yet verified). Stage 4 removed — FOH+thinking replaces response worker.
func ProcessLogSequential(ctx context.Context, app *infra.App, logUUID, logContent, timestamp, source string) (*ProcessEntryReport, error) {
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("ProcessLogSequential: app or config is nil")
	}

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

	// ── Stage 2: Refinery ────────────────────────────────────────────────────
	// Errors are propagated so Cloud Tasks returns HTTP 500 and retries on
	// transient LLM failures (rate limits, timeouts). Lost Gold is unrecoverable
	// when swallowed, whereas a retry is free.
	infra.LoggerFrom(ctx).Debug("loom stage 2: refinery", "log_uuid", logUUID)
	extractedNodeIDs, refineryErr := runRefineryPipeline(ctx, app, logUUID, logContent, timestamp)
	if refineryErr != nil {
		infra.LoggerFrom(ctx).Error("loom stage 2 FAILED: refinery pipeline — propagating for Cloud Tasks retry",
			"log_uuid", logUUID, "error", refineryErr)
		return nil, fmt.Errorf("loom stage 2: refinery: %w", refineryErr)
	}
	infra.LoggerFrom(ctx).Info("loom stage 2 done: refinery complete",
		"log_uuid", logUUID, "node_count", len(extractedNodeIDs))

	// ── Stage 3: Task Worker ──────────────────────────────────────────────────
	// Still log-and-continue: task creation has dedup guards but full retry
	// idempotency is not yet verified.
	infra.LoggerFrom(ctx).Debug("loom stage 3: task worker", "log_uuid", logUUID)
	if taskErr := runTaskWorker(ctx, app, logContent, []string{logUUID}); taskErr != nil {
		infra.LoggerFrom(ctx).Warn("loom stage 3 FAILED: task worker error — pipeline continues",
			"log_uuid", logUUID, "error", taskErr)
	} else {
		infra.LoggerFrom(ctx).Info("loom stage 3 done: task worker complete", "log_uuid", logUUID)
	}

	infra.LoggerFrom(ctx).Info("loom pipeline complete",
		"event", "loom_done",
		"log_uuid", logUUID,
	)

	return &ProcessEntryReport{
		Content:          utils.TruncateString(logContent, 500),
		Source:           source,
		ExtractedNodeIDs: extractedNodeIDs,
	}, nil
}

// ProcessEntrySyncPipeline runs refinery (stage 2) and task worker (stage 3) for an entry
// that has already been persisted by the caller. Returns the node IDs extracted by the refinery.
// Stage errors are logged but do not abort the pipeline; the caller always receives a nil error.
// Use from the unified synchronous pipeline (ProcessAndRespond); use ProcessLogSequential for
// the async Cloud Task path.
func ProcessEntrySyncPipeline(ctx context.Context, app *infra.App, logUUID, logContent, source, timestamp string) ([]string, error) {
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("ProcessEntrySyncPipeline: app or config is nil")
	}

	// ── Stage 2: Refinery ─────────────────────────────────────────────────────
	infra.LoggerFrom(ctx).Debug("loom stage 2: refinery", "log_uuid", logUUID)
	nodeIDs, refineryErr := runRefineryPipeline(ctx, app, logUUID, logContent, timestamp)
	if refineryErr != nil {
		infra.LoggerFrom(ctx).Warn("loom stage 2 FAILED: refinery pipeline error — pipeline continues",
			"log_uuid", logUUID, "error", refineryErr)
	} else {
		infra.LoggerFrom(ctx).Info("loom stage 2 done: refinery complete",
			"log_uuid", logUUID, "node_count", len(nodeIDs))
	}

	// ── Stage 3: Task Worker ──────────────────────────────────────────────────
	infra.LoggerFrom(ctx).Debug("loom stage 3: task worker", "log_uuid", logUUID)
	if taskErr := runTaskWorker(ctx, app, logContent, []string{logUUID}); taskErr != nil {
		infra.LoggerFrom(ctx).Warn("loom stage 3 FAILED: task worker error",
			"log_uuid", logUUID, "error", taskErr)
	} else {
		infra.LoggerFrom(ctx).Info("loom stage 3 done: task worker complete", "log_uuid", logUUID)
	}

	return nodeIDs, nil
}
