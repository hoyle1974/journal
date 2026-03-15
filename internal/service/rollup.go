package service

import (
	"context"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
)

// RunWeeklyRollup synthesizes the last completed week's journal analyses into a weekly_summary knowledge node.
func RunWeeklyRollup(ctx context.Context, app *infra.App) (int, error) {
	return agent.RunWeeklyRollup(ctx, app)
}

// RunMonthlyRollup synthesizes the last completed month's weekly summaries into a monthly_summary knowledge node.
func RunMonthlyRollup(ctx context.Context, app *infra.App) (int, error) {
	return agent.RunMonthlyRollup(ctx, app)
}
