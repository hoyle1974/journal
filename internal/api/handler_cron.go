package api

import (
	"net/http"

	"github.com/jackstrohm/jot/internal/infra"
)

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
