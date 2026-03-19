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
// env supplies Firestore and Config; pass from the caller (e.g. ToolEnv).
// tagMapping is an optional raw->canonical tag normalization map (from ConsolidateTags). If nil or empty, tags are used as-is.
func PromoteIncubatingClusters(ctx context.Context, env infra.ToolEnv, tagMapping map[string]string) (promoted int, err error) {
	ctx, span := infra.StartSpan(ctx, "context.promote_incubating")
	defer span.End()

	if env == nil {
		return 0, fmt.Errorf("env required")
	}
	startDate, endDate := journalDateRange(IncubationLastNDays)
	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}
	entries, err := journal.GetEntriesWithAnalysisByDateRange(ctx, client, startDate, endDate, 200)
	if err != nil {
		span.RecordError(err)
		return 0, fmt.Errorf("get entries for incubation: %w", err)
	}

	// theme (normalized, then canonicalized) -> set of dates (YYYY-MM-DD)
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
			// Apply semantic tag normalization if a mapping was provided.
			canon := norm
			if len(tagMapping) > 0 {
				if mapped, ok := tagMapping[norm]; ok && mapped != "" {
					canon = mapped
				}
			}
			if themeDays[canon] == nil {
				themeDays[canon] = make(map[string]bool)
			}
			themeDays[canon][date] = true
		}
	}

	for themeName, days := range themeDays {
		if len(days) < IncubationMinDistinctDays {
			continue
		}
		existing, _, err := FindContextByName(ctx, env, themeName)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("incubation find context failed", "theme", themeName, "error", err)
			continue
		}
		if existing != nil {
			// Touch to boost relevance so it stays in active contexts.
			if touchErr := TouchContext(ctx, env, existing.UUID, nil, 0.05); touchErr != nil {
				infra.LoggerFrom(ctx).Debug("incubation touch failed", "theme", themeName, "error", touchErr)
			} else {
				promoted++
				infra.LoggerFrom(ctx).Info("incubation touched context", "theme", themeName, "distinct_days", len(days))
			}
			continue
		}
		// Create new context with a short placeholder; it will be refined as more entries link.
		content := fmt.Sprintf("Recurring theme from journal (appeared %d days in the last %d days): %s.", len(days), IncubationLastNDays, themeName)
		if _, createErr := CreateContext(ctx, env, themeName, content, "auto", nil, nil); createErr != nil {
			infra.LoggerFrom(ctx).Warn("incubation create context failed", "theme", themeName, "error", createErr)
			continue
		}
		promoted++
		infra.LoggerFrom(ctx).Info("incubation created context", "theme", themeName, "distinct_days", len(days))
	}

	span.SetAttributes(map[string]string{"promoted": fmt.Sprintf("%d", promoted)})
	return promoted, nil
}

// CollectIncubationTags fetches the 7-day journal window and returns all unique normalized tags/categories.
// Used by the Dreamer to build the tag list for ConsolidateTags before calling PromoteIncubatingClusters.
func CollectIncubationTags(ctx context.Context, env infra.ToolEnv) ([]string, error) {
	ctx, span := infra.StartSpan(ctx, "context.collect_incubation_tags")
	defer span.End()

	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	startDate, endDate := journalDateRange(IncubationLastNDays)
	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	entries, err := journal.GetEntriesWithAnalysisByDateRange(ctx, client, startDate, endDate, 200)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("get entries for tag collection: %w", err)
	}

	seen := make(map[string]struct{})
	unique := make([]string, 0)
	for _, ew := range entries {
		for _, t := range themesFromEntry(ew) {
			norm := normalizeThemeName(t)
			if norm == "" {
				continue
			}
			if _, ok := seen[norm]; !ok {
				seen[norm] = struct{}{}
				unique = append(unique, norm)
			}
		}
	}
	infra.LoggerFrom(ctx).Debug("collect_incubation_tags: collected unique tags", "count", len(unique), "tags", strings.Join(unique, ", "))
	span.SetAttributes(map[string]string{"unique_tags": fmt.Sprintf("%d", len(unique))})
	return unique, nil
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
