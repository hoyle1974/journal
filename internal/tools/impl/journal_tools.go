package impl

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot"
	"github.com/jackstrohm/jot/tools"
)

func init() {
	registerJournalTools()
}

func registerJournalTools() {
	tools.Register(&tools.Tool{
		Name:        "get_recent_entries",
		Description: "Get the most recent journal entries (newest first). First result is the latest in time; last result is the oldest in the returned set. Use for 'recent' or 'latest' — NOT for 'oldest' or 'earliest'.",
		Category:    "journal",
		Params:      []tools.Param{tools.CountParam()},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			count := args.IntBounded("count", 10, 1, 50)
			entries, err := jot.GetEntries(ctx, count)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			result := jot.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d recent entries:\n%s", len(entries), result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_oldest_entries",
		Description: "Get the chronologically oldest journal entries (earliest by timestamp). First result is the OLDEST entry; use this when the user asks for 'oldest', 'earliest', or 'first ever' entry or memory.",
		Category:    "journal",
		Params:      []tools.Param{tools.CountParam()},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			count := args.IntBounded("count", 10, 1, 50)
			entries, err := jot.GetEntriesAsc(ctx, count)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			result := jot.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d oldest entries (chronological order):\n%s", len(entries), result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_entries_by_date_range",
		Description: "Get journal entries within a date range (newest first within range). Accepts YYYY-MM-DD or natural language (e.g. 'yesterday', 'last week', 'this morning', 'since Tuesday'). For 'oldest entry ever' use get_oldest_entries instead.",
		Category:    "journal",
		Params: []tools.Param{
			tools.RequiredStringParam("start_date", "Start date (YYYY-MM-DD or natural: yesterday, last week, this morning, since Tuesday)"),
			tools.RequiredStringParam("end_date", "End date (YYYY-MM-DD or natural: today, yesterday, last week)"),
			tools.LimitParam(50, 200),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			startDate, ok := args.RequiredString("start_date")
			if !ok {
				return tools.MissingParam("start_date")
			}
			endDate, ok := args.RequiredString("end_date")
			if !ok {
				return tools.MissingParam("end_date")
			}
			startStr, endStr, err := jot.ResolveDateRange(startDate, endDate)
			if err != nil {
				return tools.Fail("Date range error: %v", err)
			}
			limit := args.IntBounded("limit", 50, 1, 200)
			entries, err := jot.GetEntriesByDateRange(ctx, startStr, endStr, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(entries) == 0 {
				return tools.OK("No entries found between %s and %s.", startStr, endStr)
			}
			result := jot.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d entries between %s and %s:\n%s", len(entries), startStr, endStr, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "search_entries",
		Description: "Search journal entries for a keyword or phrase. Use semantic_search FIRST for factual questions.",
		Category:    "journal",
		Params: []tools.Param{
			tools.RequiredStringParam("query", "The keyword or phrase to search for"),
			tools.LimitParam(20, 50),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			query, ok := args.RequiredString("query")
			if !ok {
				return tools.MissingParam("query")
			}
			limit := args.IntBounded("limit", 20, 1, 50)
			entries, err := jot.SearchEntries(ctx, query, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(entries) == 0 {
				return tools.OK("No entries matching '%s' found.", query)
			}
			result := jot.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d entries matching '%s':\n%s", len(entries), query, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "count_entries",
		Description: "Count journal entries within a date range.",
		Category:    "journal",
		Params: []tools.Param{
			tools.OptionalStringParam("start_date", "Start date (YYYY-MM-DD, optional)"),
			tools.OptionalStringParam("end_date", "End date (YYYY-MM-DD, optional)"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			startDateStr := args.String("start_date", "")
			endDateStr := args.String("end_date", "")
			var startDate, endDate *string
			if startDateStr != "" {
				startDate = &startDateStr
			}
			if endDateStr != "" {
				endDate = &endDateStr
			}
			count, err := jot.CountEntries(ctx, startDate, endDate)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if startDate != nil || endDate != nil {
				dateRange := ""
				if startDate != nil && endDate != nil {
					dateRange = fmt.Sprintf("from %s to %s", *startDate, *endDate)
				} else if startDate != nil {
					dateRange = fmt.Sprintf("from %s", *startDate)
				} else {
					dateRange = fmt.Sprintf("until %s", *endDate)
				}
				return tools.OK("Found %d entries %s.", count, dateRange)
			}
			return tools.OK("Found %d total entries.", count)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "list_sources",
		Description: "List all unique sources (cli, sms, web, etc.) that have created journal entries.",
		Category:    "journal",
		Params:      []tools.Param{},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			entries, err := jot.GetEntries(ctx, 100)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			sourceSet := make(map[string]bool)
			for _, e := range entries {
				if e.Source != "" {
					sourceSet[e.Source] = true
				}
			}
			var sources []string
			for s := range sourceSet {
				sources = append(sources, s)
			}
			if len(sources) == 0 {
				return tools.OK("No sources found.")
			}
			return tools.OK("Sources: %s", strings.Join(sources, ", "))
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_entries_by_source",
		Description: "Get journal entries from a specific source (cli, sms, web, etc.).",
		Category:    "journal",
		Params: []tools.Param{
			tools.RequiredStringParam("source", "The source to filter by (e.g., 'cli', 'sms', 'web')"),
			tools.CountParam(),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			source, ok := args.RequiredString("source")
			if !ok {
				return tools.MissingParam("source")
			}
			count := args.IntBounded("count", 10, 1, 50)
			entries, err := jot.GetEntriesBySource(ctx, source, count)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(entries) == 0 {
				return tools.OK("No entries found from source '%s'.", source)
			}
			result := jot.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d entries from '%s':\n%s", len(entries), source, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "query_entities",
		Description: "Query extracted entities (person, project, event, place) by type, name, and status. Use when the user asks 'what do I have left for X', 'status of the party', or tasks for a specific project/event. Status values: Planned, In-Progress, Stalled, Completed. Filter by status to find incomplete work (e.g. exclude Completed).",
		Category:    "journal",
		Params: []tools.Param{
			tools.OptionalStringParam("start_date", "Start date (YYYY-MM-DD or natural: last week, yesterday). Default: 30 days ago."),
			tools.OptionalStringParam("end_date", "End date (YYYY-MM-DD or natural: today). Default: today."),
			tools.OptionalStringParam("entity_type", "Filter by type: person, project, event, place"),
			tools.OptionalStringParam("name", "Filter by entity name (substring match, case-insensitive)"),
			tools.OptionalStringParam("status", "Filter by status: Planned, In-Progress, Stalled, Completed. Omit to include all."),
			tools.LimitParam(50, 200),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			startDate := args.String("start_date", "30 days ago")
			endDate := args.String("end_date", "today")
			startStr, endStr, err := jot.ResolveDateRange(startDate, endDate)
			if err != nil {
				return tools.Fail("Date range error: %v", err)
			}
			limit := args.IntBounded("limit", 50, 1, 200)
			withAnalyses, err := jot.GetEntriesWithAnalysisByDateRange(ctx, startStr, endStr, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			entityType := strings.ToLower(strings.TrimSpace(args.String("entity_type", "")))
			nameSubstr := strings.ToLower(strings.TrimSpace(args.String("name", "")))
			statusFilter := strings.TrimSpace(args.String("status", ""))

			var lines []string
			seen := make(map[string]bool) // dedupe by "name|type|status|sourceID"
			for _, ew := range withAnalyses {
				if ew.Analysis == nil {
					continue
				}
				entryDate := ew.Entry.Timestamp
				entryDate = jot.TruncateTimestamp(entryDate, jot.DateDisplayLen)
				for _, ent := range ew.Analysis.Entities {
					if entityType != "" && strings.ToLower(ent.Type) != entityType {
						continue
					}
					if nameSubstr != "" && !strings.Contains(strings.ToLower(ent.Name), nameSubstr) {
						continue
					}
					if statusFilter != "" && ent.Status != statusFilter {
						continue
					}
					key := fmt.Sprintf("%s|%s|%s|%s", ent.Name, ent.Type, ent.Status, ent.SourceID)
					if seen[key] {
						continue
					}
					seen[key] = true
					lines = append(lines, fmt.Sprintf("- %s (%s) Status: %s [Source: %s]", ent.Name, ent.Type, ent.Status, entryDate))
				}
			}
			if len(lines) == 0 {
				return tools.OK("No matching entities found between %s and %s.", startStr, endStr)
			}
			return tools.OK("Entities between %s and %s:\n%s", startStr, endStr, strings.Join(lines, "\n"))
		},
	})
}
