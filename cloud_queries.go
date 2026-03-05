package jot

import (
	"context"

	"github.com/jackstrohm/jot/pkg/journal"
)

// QueryLog is a logged query. Re-exported from journal.
type QueryLog = journal.QueryLog

// SaveQuery saves a query and its response. If isGap is true, the query is recorded as a knowledge gap.
func SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	return journal.SaveQuery(ctx, question, answer, source, isGap)
}

// GetRecentQueries returns the most recent queries.
func GetRecentQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	return journal.GetRecentQueries(ctx, limit)
}

// SearchQueries searches past queries by keywords.
func SearchQueries(ctx context.Context, keywords string, limit int) ([]QueryLog, error) {
	return journal.SearchQueries(ctx, keywords, limit)
}

// GetRecentGapQueries returns the most recent queries marked as knowledge gaps.
func GetRecentGapQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	return journal.GetRecentGapQueries(ctx, limit)
}

// GetQueriesByDateRange returns queries within a date range.
func GetQueriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]QueryLog, error) {
	return journal.GetQueriesByDateRange(ctx, startDate, endDate, limit)
}
