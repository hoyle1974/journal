package api

import (
	"context"
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

func handleDream(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	result, err := s.Agent.RunDreamer(ctx)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("dreamer failed", "error", err)
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
	pulseResult, pulseErr := s.Agent.RunPulseAudit(ctx)
	if pulseErr != nil {
		infra.LoggerFrom(ctx).Warn("pulse audit failed after dreamer", "error", pulseErr)
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK,
		"success", true,
		"entries_processed", result.EntriesProcessed,
		"facts_extracted", result.FactsExtracted,
		"facts_written", result.FactsWritten)
	if pulseResult != nil && (pulseResult.Signals > 0 || len(pulseResult.StaleNodes) > 0) {
		infra.LoggerFrom(ctx).Info("dream pulse", "signals", pulseResult.Signals, "stale_nodes", len(pulseResult.StaleNodes))
	}
	resp := map[string]interface{}{
		"success": true, "entries_processed": result.EntriesProcessed,
		"facts_extracted": result.FactsExtracted, "facts_written": result.FactsWritten,
	}
	if pulseResult != nil {
		resp["pulse_signals"] = pulseResult.Signals
		resp["pulse_stale_nodes"] = len(pulseResult.StaleNodes)
	}
	WriteJSON(w, http.StatusOK, resp)
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
