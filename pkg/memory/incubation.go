// Package memory provides momentum/incubation: promote recurring fact clusters to formal contexts.
package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/journal"
)

const (
	// IncubationLastNDays is the number of days to look back for recurring themes.
	IncubationLastNDays = 7
	// IncubationMinDistinctDays is the minimum distinct days a theme must appear to be promoted.
	IncubationMinDistinctDays = 2
)

// PromoteIncubatingClusters looks at recent journal entries over the last N days, counts distinct days per theme (tags and category), and creates or touches a context for each theme that appears on at least MinDistinctDays. Call from the Dreamer after consolidation.
func PromoteIncubatingClusters(ctx context.Context) (promoted int, err error) {
	ctx, span := infra.StartSpan(ctx, "context.promote_incubating")
	defer span.End()

	startDate, endDate := journalDateRange(IncubationLastNDays)
	entries, err := journal.GetEntriesWithAnalysisByDateRange(ctx, startDate, endDate, 200)
	if err != nil {
		span.RecordError(err)
		return 0, fmt.Errorf("get entries for incubation: %w", err)
	}

	// theme (normalized) -> set of dates (YYYY-MM-DD)
	themeDays := make(map[string]map[string]bool)
	for _, ew := range entries {
		date := dateFromTimestamp(ew.Entry.Timestamp)
		if date == "" {
			continue
		}
		themes := themesFromEntry(ew)
		for _, t := range themes {
			norm := normalizeThemeName(t)
			if norm == "" {
				continue
			}
			if themeDays[norm] == nil {
				themeDays[norm] = make(map[string]bool)
			}
			themeDays[norm][date] = true
		}
	}

	for themeName, days := range themeDays {
		if len(days) < IncubationMinDistinctDays {
			continue
		}
		existing, _, err := FindContextByName(ctx, themeName)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("incubation find context failed", "theme", themeName, "error", err)
			continue
		}
		if existing != nil {
			// Touch to boost relevance so it stays in active contexts.
			if touchErr := TouchContext(ctx, existing.UUID, nil, 0.05); touchErr != nil {
				infra.LoggerFrom(ctx).Debug("incubation touch failed", "theme", themeName, "error", touchErr)
			} else {
				promoted++
				infra.LoggerFrom(ctx).Info("incubation touched context", "theme", themeName, "distinct_days", len(days))
			}
			continue
		}
		// Create new context with a short placeholder; it will be refined as more entries link.
		content := fmt.Sprintf("Recurring theme from journal (appeared %d days in the last %d days): %s.", len(days), IncubationLastNDays, themeName)
		if _, createErr := CreateContext(ctx, themeName, content, "auto", nil, nil); createErr != nil {
			infra.LoggerFrom(ctx).Warn("incubation create context failed", "theme", themeName, "error", createErr)
			continue
		}
		promoted++
		infra.LoggerFrom(ctx).Info("incubation created context", "theme", themeName, "distinct_days", len(days))
	}

	span.SetAttributes(map[string]string{"promoted": fmt.Sprintf("%d", promoted)})
	return promoted, nil
}

func journalDateRange(lastNDays int) (start, end string) {
	// Use journal date format YYYY-MM-DD; GetEntriesWithAnalysisByDateRange appends T00:00:00 / T23:59:59.
	now := time.Now()
	end = now.Format("2006-01-02")
	start = now.AddDate(0, 0, -lastNDays).Format("2006-01-02")
	return start, end
}

func dateFromTimestamp(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ""
}

func themesFromEntry(ew journal.EntryWithAnalysis) []string {
	var out []string
	if ew.Analysis != nil {
		if c := strings.TrimSpace(strings.ToLower(ew.Analysis.Category)); c != "" {
			out = append(out, c)
		}
		for _, t := range ew.Analysis.Tags {
			t = strings.TrimSpace(strings.ToLower(t))
			if t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

func normalizeThemeName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "_")
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}
