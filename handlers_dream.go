package jot

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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

// handlePendingQuestions returns unresolved pending questions (GET /pending-questions).
func handlePendingQuestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	ctx := r.Context()
	limit := 20
	questions, err := GetUnresolvedPendingQuestions(ctx, limit)
	if err != nil {
		LoggerFrom(ctx).Error("pending questions list failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"questions": questions,
		"count":     len(questions),
	})
}

// handleRollup runs weekly and monthly roll-up (POST /rollup). Call weekly from cron/scheduler.
func handleRollup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	ctx := r.Context()
	LoggerFrom(ctx).Info("rollup started")
	weeklyEntries, err := RunWeeklyRollup(ctx)
	if err != nil {
		LoggerFrom(ctx).Error("weekly rollup failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	monthlyNodes, err := RunMonthlyRollup(ctx)
	if err != nil {
		LoggerFrom(ctx).Error("monthly rollup failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LoggerFrom(ctx).Info("rollup completed", "weekly_entries", weeklyEntries, "monthly_weekly_nodes", monthlyNodes)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":               true,
		"weekly_entries_rolled": weeklyEntries,
		"monthly_weekly_nodes":  monthlyNodes,
	})
}

// handlePendingQuestionResolve resolves one pending question (POST /pending-questions/:id/resolve).
func handlePendingQuestionResolve(w http.ResponseWriter, r *http.Request, questionID string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	ctx := r.Context()
	var body struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if err := ResolvePendingQuestion(ctx, questionID, strings.TrimSpace(body.Answer)); err != nil {
		LoggerFrom(ctx).Error("resolve pending question failed", "id", questionID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": questionID})
}
