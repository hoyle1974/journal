package service

import (
	"context"

	"github.com/jackstrohm/jot/pkg/agent"
)

// RunWeeklyRollup synthesizes the last completed week's journal analyses into a weekly_summary knowledge node.
func RunWeeklyRollup(ctx context.Context) (int, error) {
	return agent.RunWeeklyRollup(ctx, ServiceEnv{})
}

// RunMonthlyRollup synthesizes the last completed month's weekly summaries into a monthly_summary knowledge node.
func RunMonthlyRollup(ctx context.Context) (int, error) {
	return agent.RunMonthlyRollup(ctx, ServiceEnv{})
}
