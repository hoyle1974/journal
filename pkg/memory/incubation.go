// Package memory provides momentum/incubation: promote recurring fact clusters to formal contexts.
package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	// IncubationLastNDays is the number of days to look back for recurring themes.
	IncubationLastNDays = 7
	// IncubationMinDistinctDays is the minimum distinct days a theme must appear to be promoted.
	IncubationMinDistinctDays = 2
)

// PromoteIncubatingClusters looks at recent journal entries over the last N days, counts distinct days per theme (tags and category), and creates or touches a context for each theme that appears on at least MinDistinctDays. Call from the Dreamer after consolidation.
// tagMapping is an optional raw->canonical tag normalization map (from ConsolidateTags). If nil or empty, tags are used as-is.
func (s *Store) PromoteIncubatingClusters(ctx context.Context, tagMapping map[string]string) (promoted int, err error) {
	startDate, endDate := journalDateRange(IncubationLastNDays)
	entries, err := s.GetEntriesWithAnalysisByDateRange(ctx, startDate, endDate, 200)
	if err != nil {
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
		// TODO(batch-2): FindContextByName will be converted to a Store method; pass nil env for now.
		existing, _, err := FindContextByName(ctx, nil, themeName)
		if err != nil {
			s.log.Warn("incubation find context failed", "theme", themeName, "error", err)
			continue
		}
		if existing != nil {
			// Touch to boost relevance so it stays in active contexts.
			// TODO(batch-2): TouchContext will be converted to a Store method; pass nil env for now.
			if touchErr := TouchContext(ctx, nil, existing.UUID, nil, 0.05); touchErr != nil {
				s.log.Debug("incubation touch failed", "theme", themeName, "error", touchErr)
			} else {
				promoted++
				s.log.Info("incubation touched context", "theme", themeName, "distinct_days", len(days))
			}
			continue
		}
		// Create new context with a short placeholder; it will be refined as more entries link.
		content := fmt.Sprintf("Recurring theme from journal (appeared %d days in the last %d days): %s.", len(days), IncubationLastNDays, themeName)
		// TODO(batch-2): CreateContext will be converted to a Store method; pass nil env for now.
		if _, createErr := CreateContext(ctx, nil, themeName, content, "auto", nil, nil); createErr != nil {
			s.log.Warn("incubation create context failed", "theme", themeName, "error", createErr)
			continue
		}
		promoted++
		s.log.Info("incubation created context", "theme", themeName, "distinct_days", len(days))
	}

	return promoted, nil
}

// CollectIncubationTags fetches the 7-day journal window and returns all unique normalized tags/categories.
// Used by the Dreamer to build the tag list for ConsolidateTags before calling PromoteIncubatingClusters.
func (s *Store) CollectIncubationTags(ctx context.Context) ([]string, error) {
	startDate, endDate := journalDateRange(IncubationLastNDays)
	entries, err := s.GetEntriesWithAnalysisByDateRange(ctx, startDate, endDate, 200)
	if err != nil {
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
	s.log.Debug("collect_incubation_tags: collected unique tags", "count", len(unique), "tags", strings.Join(unique, ", "))
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

func themesFromEntry(ew EntryWithAnalysis) []string {
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
