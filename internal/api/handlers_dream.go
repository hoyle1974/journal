package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackstrohm/jot/pkg/infra"
)

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
	result, err := s.Backend.RunDreamer(ctx)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("dreamer failed", "error", err)
		code := http.StatusInternalServerError
		if s.Backend.IsLLMQuotaOrBillingError(err) {
			code = http.StatusTooManyRequests
		} else if s.Backend.IsLLMPermissionOrBillingDenied(err) {
			code = http.StatusForbidden
		}
		LogHandlerResponse(ctx, r.Method, path, code, "error", err.Error())
		WriteJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	pulseResult, pulseErr := s.Backend.RunPulseAudit(ctx)
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
	deleted, err := s.Backend.RunJanitor(ctx)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("janitor failed", "error", err)
		code := http.StatusInternalServerError
		if s.Backend.IsLLMQuotaOrBillingError(err) {
			code = http.StatusTooManyRequests
		} else if s.Backend.IsLLMPermissionOrBillingDenied(err) {
			code = http.StatusForbidden
		}
		LogHandlerResponse(ctx, r.Method, path, code, "error", err.Error())
		WriteJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "success", true, "deleted", deleted)
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "deleted": deleted})
}

func handlePendingQuestions(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodGet {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	questions, err := s.Backend.GetUnresolvedPendingQuestions(ctx, 20)
	if err != nil {
		infra.LoggerFrom(ctx).Error("pending questions list failed", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "count", len(questions))
	WriteJSON(w, http.StatusOK, map[string]interface{}{"questions": questions, "count": len(questions)})
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
	weeklyEntries, err := s.Backend.RunWeeklyRollup(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("weekly rollup failed", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	monthlyNodes, err := s.Backend.RunMonthlyRollup(ctx)
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

func handlePendingQuestionResolve(s *Server, w http.ResponseWriter, r *http.Request, questionID string) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path, "question_id", questionID)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var body struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "Invalid JSON body")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if err := s.Backend.ResolvePendingQuestion(ctx, questionID, strings.TrimSpace(body.Answer)); err != nil {
		infra.LoggerFrom(ctx).Error("resolve pending question failed", "id", questionID, "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "success", true, "id", questionID)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": questionID})
}
