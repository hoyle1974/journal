package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/persona"
)

// handleDreamLatest serves GET /dream/latest: returns the latest dream narrative and optionally marks it read.
func handleDreamLatest(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	latest, err := s.System.GetLatestDream(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("dream latest: firestore", "error", err)
		return nil, err
	}
	if latest == nil {
		return map[string]interface{}{
			"narrative": "",
			"unread":    false,
			"timestamp": "",
		}, nil
	}
	narrative := latest.Narrative
	timestamp := latest.Timestamp
	unread := latest.Unread
	if narrative != "" {
		if app, ok := s.App.(*infra.App); ok {
			narrative = persona.Apply(ctx, app, narrative, "")
		}
	}
	if markRead := r.URL.Query().Get("mark_read"); markRead == "true" && unread {
		_ = s.System.MarkLatestDreamRead(ctx)
		unread = false
	}
	return map[string]interface{}{
		"narrative": narrative,
		"unread":    unread,
		"timestamp": timestamp,
	}, nil
}

// dreamRunProgress writes phase and log to Firestore for async dream run polling.
type dreamRunProgress struct {
	system SystemService
	runID  string
}

func (p *dreamRunProgress) OnPhase(ctx context.Context, phase string) {
	_ = p.system.UpdateDreamRunPhase(ctx, p.runID, phase, "")
}

func (p *dreamRunProgress) OnLog(ctx context.Context, msg string) {
	_ = p.system.AppendDreamRunLog(ctx, p.runID, msg)
}

func handleDream(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	runID := infra.GenShortRunID()
	acquired, existingRunID, err := s.System.TryAcquireDreamRunLock(ctx, runID)
	if err != nil {
		infra.LoggerFrom(ctx).Error("dream lock failed", "error", err)
		return nil, err
	}
	idToReturn := runID
	alreadyRunning := false
	if !acquired {
		idToReturn = existingRunID
		alreadyRunning = true
		infra.LoggerFrom(ctx).Info("dream already in progress", "dream_run_id", existingRunID)
	} else {
		payload := map[string]interface{}{"dream_run_id": runID}
		if enqErr := s.App.EnqueueTask(ctx, "/internal/dream-run", payload); enqErr != nil {
			infra.LoggerFrom(ctx).Warn("dream task enqueue failed, running in goroutine", "error", enqErr)
			go runDreamInBackground(context.WithoutCancel(ctx), s, runID)
		}
	}
	return withStatus(http.StatusAccepted, map[string]interface{}{
		"dream_run_id":    idToReturn,
		"already_running": alreadyRunning,
		"message":         "Dream run started. Poll GET /dream/status for progress.",
	}), nil
}

// runDreamInBackground runs the dreamer with progress and updates Firestore (used when Cloud Tasks is unavailable).
func runDreamInBackground(ctx context.Context, s *Server, runID string) {
	// Use a long timeout so the dream can complete; Cloud Run request may still time out if this was the HTTP handler.
	runCtx, cancel := context.WithTimeout(ctx, 55*time.Minute)
	defer cancel()
	progress := &dreamRunProgress{system: s.System, runID: runID}
	result, err := s.Agent.RunDreamerWithProgress(runCtx, runID, progress)
	if err != nil {
		_ = s.System.SetDreamRunFailed(runCtx, runID, err.Error())
		infra.LoggerFrom(runCtx).Error("dream run failed", "dream_run_id", runID, "error", err)
		return
	}
	_ = s.System.SetDreamRunCompleted(runCtx, runID, map[string]interface{}{
		"entries_processed":    result.EntriesProcessed,
		"facts_extracted":      result.FactsExtracted,
		"facts_written":        result.FactsWritten,
		"contexts_synthesized": result.ContextsSynthesized,
	})
	if _, pulseErr := s.Agent.RunPulseAudit(runCtx); pulseErr != nil {
		infra.LoggerFrom(runCtx).Warn("pulse audit failed after dream", "error", pulseErr)
	}
}

func handleDreamRun(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	var body struct {
		DreamRunID string `json:"dream_run_id" validate:"required"`
	}
	if err := DecodeAndValidate(r, &body, s.Validator); err != nil {
		infra.LoggerFrom(ctx).Warn("dream-run invalid body", "error", err)
		return nil, handlerError(http.StatusBadRequest, err.Error())
	}
	runID := body.DreamRunID

	// Guard against stale/duplicate Cloud Task deliveries. If the lock's current run_id no longer
	// matches, this task was superseded (e.g. lock expired + new run started). Return 200 so Cloud
	// Tasks stops retrying; do not run the dreamer again.
	if state, stateErr := s.System.GetDreamRunState(ctx); stateErr == nil && state != nil {
		if state.DreamRunID != runID {
			infra.LoggerFrom(ctx).Warn("dream-run task is stale, skipping", "task_run_id", runID, "current_run_id", state.DreamRunID)
			return map[string]interface{}{"success": true, "skipped": true, "reason": "stale task"}, nil
		}
	}

	// Long timeout so the dream can complete (Cloud Run allows up to 60 min).
	runCtx, cancel := context.WithTimeout(ctx, 55*time.Minute)
	defer cancel()
	_ = s.System.UpdateDreamRunPhase(runCtx, runID, "running", "Dream run started.")
	progress := &dreamRunProgress{system: s.System, runID: runID}
	result, err := s.Agent.RunDreamerWithProgress(runCtx, runID, progress)
	if err != nil {
		_ = s.System.SetDreamRunFailed(runCtx, runID, err.Error())
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("dream run failed", "dream_run_id", runID, "error", err)
		// Cloud Tasks expects 2xx to avoid retries; return success=false with 200.
		return map[string]interface{}{"success": false, "error": err.Error()}, nil
	}
	_ = s.System.SetDreamRunCompleted(runCtx, runID, map[string]interface{}{
		"entries_processed":    result.EntriesProcessed,
		"facts_extracted":      result.FactsExtracted,
		"facts_written":        result.FactsWritten,
		"contexts_synthesized": result.ContextsSynthesized,
	})
	if _, pulseErr := s.Agent.RunPulseAudit(runCtx); pulseErr != nil {
		infra.LoggerFrom(ctx).Warn("pulse audit failed after dream", "error", pulseErr)
	}
	return map[string]interface{}{
		"success": true, "dream_run_id": runID,
		"entries_processed": result.EntriesProcessed, "facts_extracted": result.FactsExtracted,
		"facts_written": result.FactsWritten, "contexts_synthesized": result.ContextsSynthesized,
	}, nil
}

func handleDreamStatus(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	state, err := s.System.GetDreamRunState(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("dream status failed", "error", err)
		return nil, err
	}
	if state == nil {
		return map[string]interface{}{
			"status": "", "dream_run_id": "", "message": "No dream run yet.",
		}, nil
	}
	return state, nil
}

func handleJanitor(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	deleted, err := s.Agent.RunJanitor(ctx)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("janitor failed", "error", err)
		code := http.StatusInternalServerError
		if infra.IsLLMQuotaOrBillingError(err) {
			code = http.StatusTooManyRequests
		} else if infra.IsLLMPermissionOrBillingDenied(err) {
			code = http.StatusForbidden
		}
		return nil, handlerError(code, err.Error())
	}
	return map[string]interface{}{"success": true, "deleted": deleted}, nil
}

func handleRollup(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	weeklyEntries, err := s.Agent.RunWeeklyRollup(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("weekly rollup failed", "error", err)
		return nil, err
	}
	monthlyNodes, err := s.Agent.RunMonthlyRollup(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("monthly rollup failed", "error", err)
		return nil, err
	}
	return map[string]interface{}{
		"success": true, "weekly_entries_rolled": weeklyEntries, "monthly_weekly_nodes": monthlyNodes,
	}, nil
}

func handleDecayContexts(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	if err := s.Memory.InitializePermanentContexts(ctx); err != nil {
		infra.LoggerFrom(ctx).Warn("failed to initialize permanent contexts", "error", err)
	}
	decayedCount, err := s.Memory.DecayContexts(ctx)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("decay-contexts failed", "error", err)
		return nil, err
	}
	return map[string]interface{}{"success": true, "decayed_count": decayedCount}, nil
}
