package service

import (
	"context"

	"github.com/jackstrohm/jot/internal/agent"
)

// RunWeeklyRollup synthesizes the last completed week's journal analyses into a weekly_summary knowledge node.
// Caller must set app on context (e.g. infra.WithApp(ctx, app)) when invoking.
func RunWeeklyRollup(ctx context.Context) (int, error) {
	return agent.RunWeeklyRollup(ctx)
}

// RunMonthlyRollup synthesizes the last completed month's weekly summaries into a monthly_summary knowledge node.
// Caller must set app on context (e.g. infra.WithApp(ctx, app)) when invoking.
func RunMonthlyRollup(ctx context.Context) (int, error) {
	return agent.RunMonthlyRollup(ctx)
}
