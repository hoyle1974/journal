package service

import (
	"context"
	"fmt"
	"os"

	"github.com/jackstrohm/jot/internal/infra"
)

const (
	JanitorWeightThreshold   = 0.2
	JanitorStaleDays         = 30
	PulseStaleDays           = 14
	PulseImportanceThreshold = 0.7
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// PulseResult holds the outcome of a pulse audit run.
type PulseResult struct {
	StaleNodes []string
	Signals    int
}

// RunJanitor performs garbage collection on semantic memory via pkg/memory.
func RunJanitor(ctx context.Context, app *infra.App) (int, error) {
	ctx, span := infra.StartSpan(ctx, "cron.janitor")
	defer span.End()

	deleted, err := app.Memory.EvictStaleNodes(ctx, JanitorWeightThreshold, JanitorStaleDays)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("janitor completed", "deleted", deleted)
	span.SetAttributes(map[string]string{"deleted": fmt.Sprintf("%d", deleted)})
	return deleted, nil
}

// RunPulseAudit identifies high-value nodes that have not been recalled in PulseStaleDays and creates a proactive signal for each via pkg/memory.
func RunPulseAudit(ctx context.Context, app *infra.App) (*PulseResult, error) {
	ctx, span := infra.StartSpan(ctx, "cron.pulse_audit")
	defer span.End()

	r, err := app.Memory.CreatePulseAuditSignals(ctx, PulseImportanceThreshold, PulseStaleDays)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	result := &PulseResult{StaleNodes: r.StaleNodes, Signals: r.Signals}
	span.SetAttributes(map[string]string{
		"stale_nodes": fmt.Sprintf("%d", len(result.StaleNodes)),
		"signals":     fmt.Sprintf("%d", result.Signals),
	})
	return result, nil
}
