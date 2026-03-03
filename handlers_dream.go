package jot

import (
	"context"
	"net/http"
	"time"
)

func handleDream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	ctx := r.Context()
	LoggerFrom(ctx).Info("dream started")

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	result, err := RunDreamer(ctx)
	if err != nil {
		ErrorsTotal.Inc()
		LoggerFrom(ctx).Error("dreamer failed", "error", err)
		code := http.StatusInternalServerError
		if IsLLMQuotaOrBillingError(err) {
			code = http.StatusTooManyRequests
		} else if IsLLMPermissionOrBillingDenied(err) {
			code = http.StatusForbidden
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}

	pulseResult, pulseErr := RunPulseAudit(ctx)
	if pulseErr != nil {
		LoggerFrom(ctx).Warn("pulse audit failed after dreamer", "error", pulseErr)
	}

	LoggerFrom(ctx).Info("dream completed", "entries_processed", result.EntriesProcessed, "facts_extracted", result.FactsExtracted, "facts_written", result.FactsWritten)
	if pulseResult != nil && (pulseResult.Signals > 0 || len(pulseResult.StaleNodes) > 0) {
		LoggerFrom(ctx).Info("dream pulse", "signals", pulseResult.Signals, "stale_nodes", len(pulseResult.StaleNodes))
	}

	resp := map[string]interface{}{
		"success":           true,
		"entries_processed": result.EntriesProcessed,
		"facts_extracted":   result.FactsExtracted,
		"facts_written":     result.FactsWritten,
	}
	if pulseResult != nil {
		resp["pulse_signals"] = pulseResult.Signals
		resp["pulse_stale_nodes"] = len(pulseResult.StaleNodes)
	}

	writeJSON(w, http.StatusOK, resp)
}

func handleJanitor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	ctx := r.Context()
	LoggerFrom(ctx).Info("janitor started")
	deleted, err := RunJanitor(ctx)
	if err != nil {
		ErrorsTotal.Inc()
		LoggerFrom(ctx).Error("janitor failed", "error", err)
		code := http.StatusInternalServerError
		if IsLLMQuotaOrBillingError(err) {
			code = http.StatusTooManyRequests
		} else if IsLLMPermissionOrBillingDenied(err) {
			code = http.StatusForbidden
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	LoggerFrom(ctx).Info("janitor completed", "deleted", deleted)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"deleted": deleted,
	})
}
