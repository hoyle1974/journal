package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/infra"
)

const systemCollection = "_system"
const latestDreamDoc = "latest_dream"

// handleDreamLatest serves GET /dream/latest: returns the latest dream narrative and optionally marks it read.
func handleDreamLatest(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodGet {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	client, err := s.App.Firestore(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("dream latest: firestore", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	doc, err := client.Collection(systemCollection).Doc(latestDreamDoc).Get(ctx)
	if err != nil || !doc.Exists() {
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK)
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"narrative": "",
			"unread":    false,
			"timestamp": "",
		})
		return
	}
	data := doc.Data()
	narrative, _ := data["narrative"].(string)
	timestamp, _ := data["timestamp"].(string)
	unread, _ := data["unread"].(bool)
	if markRead := r.URL.Query().Get("mark_read"); markRead == "true" && unread {
		_, _ = client.Collection(systemCollection).Doc(latestDreamDoc).Update(ctx, []firestore.Update{
			{Path: "unread", Value: false},
		})
		unread = false
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK)
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"narrative": narrative,
		"unread":    unread,
		"timestamp": timestamp,
	})
}

// dreamRunProgress writes phase and log to Firestore for async dream run polling.
type dreamRunProgress struct {
	app   dreamRunStateApp
	runID string
}

func (p *dreamRunProgress) OnPhase(ctx context.Context, phase string) {
	_ = UpdateDreamRunPhase(ctx, p.app, p.runID, phase, "")
}

func (p *dreamRunProgress) OnLog(ctx context.Context, msg string) {
	_ = AppendDreamRunLog(ctx, p.app, p.runID, msg)
}

func handleDream(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	runID := infra.GenShortRunID()
	acquired, existingRunID, err := TryAcquireDreamRunLock(ctx, s.App, runID)
	if err != nil {
		infra.LoggerFrom(ctx).Error("dream lock failed", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
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
	LogHandlerResponse(ctx, r.Method, path, http.StatusAccepted)
	WriteJSON(w, http.StatusAccepted, map[string]interface{}{
		"dream_run_id":     idToReturn,
		"already_running":  alreadyRunning,
		"message":          "Dream run started. Poll GET /dream/status for progress.",
	})
}

// runDreamInBackground runs the dreamer with progress and updates Firestore (used when Cloud Tasks is unavailable).
func runDreamInBackground(ctx context.Context, s *Server, runID string) {
	// Use a long timeout so the dream can complete; Cloud Run request may still time out if this was the HTTP handler.
	runCtx, cancel := context.WithTimeout(ctx, 55*time.Minute)
	defer cancel()
	progress := &dreamRunProgress{app: s.App, runID: runID}
	result, err := s.Agent.RunDreamerWithProgress(runCtx, runID, progress)
	if err != nil {
		_ = SetDreamRunFailed(runCtx, s.App, runID, err.Error())
		infra.LoggerFrom(runCtx).Error("dream run failed", "dream_run_id", runID, "error", err)
		return
	}
	_ = SetDreamRunCompleted(runCtx, s.App, runID, map[string]interface{}{
		"entries_processed":    result.EntriesProcessed,
		"facts_extracted":       result.FactsExtracted,
		"facts_written":         result.FactsWritten,
		"contexts_synthesized": result.ContextsSynthesized,
	})
	if _, pulseErr := s.Agent.RunPulseAudit(runCtx); pulseErr != nil {
		infra.LoggerFrom(runCtx).Warn("pulse audit failed after dream", "error", pulseErr)
	}
}

func handleDreamRun(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var body struct {
		DreamRunID string `json:"dream_run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DreamRunID == "" {
		infra.LoggerFrom(ctx).Warn("dream-run invalid body", "error", err)
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "missing dream_run_id"})
		return
	}
	runID := body.DreamRunID
	// Long timeout so the dream can complete (Cloud Run allows up to 60 min).
	runCtx, cancel := context.WithTimeout(ctx, 55*time.Minute)
	defer cancel()
	_ = UpdateDreamRunPhase(runCtx, s.App, runID, "running", "Dream run started.")
	progress := &dreamRunProgress{app: s.App, runID: runID}
	result, err := s.Agent.RunDreamerWithProgress(runCtx, runID, progress)
	if err != nil {
		_ = SetDreamRunFailed(runCtx, s.App, runID, err.Error())
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("dream run failed", "dream_run_id", runID, "error", err)
		WriteJSON(w, http.StatusOK, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	_ = SetDreamRunCompleted(runCtx, s.App, runID, map[string]interface{}{
		"entries_processed":    result.EntriesProcessed,
		"facts_extracted":      result.FactsExtracted,
		"facts_written":         result.FactsWritten,
		"contexts_synthesized": result.ContextsSynthesized,
	})
	if _, pulseErr := s.Agent.RunPulseAudit(runCtx); pulseErr != nil {
		infra.LoggerFrom(ctx).Warn("pulse audit failed after dream", "error", pulseErr)
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK)
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "dream_run_id": runID,
		"entries_processed": result.EntriesProcessed, "facts_extracted": result.FactsExtracted,
		"facts_written": result.FactsWritten, "contexts_synthesized": result.ContextsSynthesized,
	})
}

func handleDreamStatus(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodGet {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	state, err := GetDreamRunState(ctx, s.App)
	if err != nil {
		infra.LoggerFrom(ctx).Error("dream status failed", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if state == nil {
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"status": "", "dream_run_id": "", "message": "No dream run yet.",
		})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK)
	WriteJSON(w, http.StatusOK, state)
}

func handleJanitor(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
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
		LogHandlerResponse(ctx, r.Method, path, code, "error", err.Error())
		WriteJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "success", true, "deleted", deleted)
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "deleted": deleted})
}

func handleRollup(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	weeklyEntries, err := s.Agent.RunWeeklyRollup(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("weekly rollup failed", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	monthlyNodes, err := s.Agent.RunMonthlyRollup(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("monthly rollup failed", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "success", true, "weekly_entries_rolled", weeklyEntries, "monthly_weekly_nodes", monthlyNodes)
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "weekly_entries_rolled": weeklyEntries, "monthly_weekly_nodes": monthlyNodes,
	})
}

func handleDecayContexts(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	if err := s.Memory.InitializePermanentContexts(ctx); err != nil {
		infra.LoggerFrom(ctx).Warn("failed to initialize permanent contexts", "error", err)
	}
	decayedCount, err := s.Memory.DecayContexts(ctx)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("decay-contexts failed", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "success", true, "decayed_count", decayedCount)
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "decayed_count": decayedCount})
}
