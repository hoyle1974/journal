package impl

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/utils"
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
			entries, err := journal.GetEntries(ctx, count)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			result := journal.FormatEntriesForContext(entries, 10000)
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
			entries, err := journal.GetEntriesAsc(ctx, count)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			result := journal.FormatEntriesForContext(entries, 10000)
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
			startStr, endStr, err := resolveToolDateRange(startDate, endDate)
			if err != nil {
				return tools.Fail("Date range error: %v", err)
			}
			limit := args.IntBounded("limit", 50, 1, 200)
			entries, err := journal.GetEntriesByDateRange(ctx, startStr, endStr, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(entries) == 0 {
				return tools.OK("No entries found between %s and %s.", startStr, endStr)
			}
			result := journal.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d entries between %s and %s:\n%s", len(entries), startStr, endStr, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "search_entries",
		Description: "Search journal entries for a keyword or phrase. Use semantic_search FIRST for factual questions. Optional category filters by analysis category (work, personal, health, finance, logistics). Limitation: when category is used, only the last calendar month of entries is searched.",
		Category:    "journal",
		Params: []tools.Param{
			tools.RequiredStringParam("query", "The keyword or phrase to search for"),
			tools.OptionalStringParam("category", "Filter by category: work, personal, health, finance, logistics (requires entries to have been analyzed)"),
			tools.LimitParam(20, 50),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			query, ok := args.RequiredString("query")
			if !ok {
				return tools.MissingParam("query")
			}
			limit := args.IntBounded("limit", 20, 1, 50)
			categoryFilter := strings.ToLower(strings.TrimSpace(args.String("category", "")))
			var entries []journal.Entry
			if categoryFilter != "" {
				startStr, endStr, err := resolveToolDateRange("last month", "today")
				if err != nil {
					return tools.Fail("Date range error: %v", err)
				}
				withAnalyses, err := journal.GetEntriesWithAnalysisByDateRange(ctx, startStr, endStr, 200)
				if err != nil {
					return tools.Fail("Error: %v", err)
				}
				queryLower := strings.ToLower(query)
				for _, ew := range withAnalyses {
					if ew.Analysis == nil || ew.Analysis.Category != categoryFilter {
						continue
					}
					if !strings.Contains(strings.ToLower(ew.Entry.Content), queryLower) {
						continue
					}
					entries = append(entries, ew.Entry)
					if len(entries) >= limit {
						break
					}
				}
			} else {
				var err error
				entries, err = journal.SearchEntries(ctx, query, limit)
				if err != nil {
					return tools.Fail("Error: %v", err)
				}
			}
			if len(entries) == 0 {
				return tools.OK("No entries matching '%s' found.", query)
			}
			result := journal.FormatEntriesForContext(entries, 10000)
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
			count, err := journal.CountEntries(ctx, startDate, endDate)
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
			entries, err := journal.GetEntries(ctx, 100)
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
			entries, err := journal.GetEntriesBySource(ctx, source, count)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(entries) == 0 {
				return tools.OK("No entries found from source '%s'.", source)
			}
			result := journal.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d entries from '%s':\n%s", len(entries), source, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "query_activity_history",
		Description: "Get a thematic, chronological summary of journal entries about a topic (e.g. migraines, work stress, a project). Use when the user asks 'how have my X been?', 'what's been going on with Y?', or for a distilled timeline. Fetches a larger batch and uses an LLM to produce a concise summary. Limitation: optional timeframe must use supported expressions only—YYYY-MM-DD, 'last month', 'last week', 'yesterday', 'today'; phrases like 'last 6 months' or 'last 30 days' are not supported and will error.",
		Category:    "journal",
		Params: []tools.Param{
			tools.RequiredStringParam("topic", "The topic or keyword to summarize (e.g. 'migraines', 'work stress', 'jot app')"),
			tools.OptionalStringParam("timeframe", "Optional timeframe: 'last 6 months', 'last 30 days', 'this year', or leave empty for all matching entries (up to 100)"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			topic, ok := args.RequiredString("topic")
			if !ok {
				return tools.MissingParam("topic")
			}
			timeframe := args.String("timeframe", "")
			var entries []journal.Entry
			if timeframe != "" {
				startStr, endStr, err := resolveToolDateRange(timeframe, "today")
				if err != nil {
					return tools.Fail("Invalid timeframe: %v", err)
				}
				byDate, err := journal.GetEntriesByDateRange(ctx, startStr, endStr, 200)
				if err != nil {
					return tools.Fail("Error fetching entries: %v", err)
				}
				topicLower := strings.ToLower(topic)
				for _, e := range byDate {
					if strings.Contains(strings.ToLower(e.Content), topicLower) {
						entries = append(entries, e)
					}
				}
			} else {
				var err error
				entries, err = journal.SearchEntries(ctx, topic, 100)
				if err != nil {
					return tools.Fail("Error searching entries: %v", err)
				}
			}
			if len(entries) == 0 {
				return tools.OK("No entries found for topic '%s'.", topic)
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Timestamp < entries[j].Timestamp
			})
			entriesText := journal.FormatEntriesForContext(entries, 12000)
			app := infra.GetApp(ctx)
			if app == nil || app.Config() == nil {
				return tools.Fail("App not available for summarization")
			}
			userPrompt, err := prompts.BuildActivityHistory(prompts.ActivityHistoryData{
				Topic:       topic,
				Timeframe:   timeframe,
				EntriesText: utils.WrapAsUserData(utils.SanitizePrompt(entriesText)),
			})
			if err != nil {
				return tools.Fail("Failed to build activity history prompt: %v", err)
			}
			systemPrompt := prompts.DataSafety()
			summary, err := infra.GenerateContentSimple(ctx, systemPrompt, userPrompt, app.Config(), &infra.GenConfig{MaxOutputTokens: 1024})
			if err != nil {
				return tools.Fail("Summarization failed: %v", err)
			}
			return tools.OK("Activity history for '%s'%s:\n\n%s", topic, func() string {
				if timeframe != "" {
					return " (" + timeframe + ")"
				}
				return ""
			}(), strings.TrimSpace(summary))
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
			tools.OptionalStringParam("category", "Filter by entry category: work, personal, health, finance, logistics"),
			tools.LimitParam(50, 200),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			startDate := args.String("start_date", "30 days ago")
			endDate := args.String("end_date", "today")
			startStr, endStr, err := resolveToolDateRange(startDate, endDate)
			if err != nil {
				return tools.Fail("Date range error: %v", err)
			}
			limit := args.IntBounded("limit", 50, 1, 200)
			withAnalyses, err := journal.GetEntriesWithAnalysisByDateRange(ctx, startStr, endStr, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			entityType := strings.ToLower(strings.TrimSpace(args.String("entity_type", "")))
			nameSubstr := strings.ToLower(strings.TrimSpace(args.String("name", "")))
			statusFilter := strings.TrimSpace(args.String("status", ""))
			categoryFilter := strings.ToLower(strings.TrimSpace(args.String("category", "")))

			var lines []string
			seen := make(map[string]bool) // dedupe by "name|type|status|sourceID"
			for _, ew := range withAnalyses {
				if ew.Analysis == nil {
					continue
				}
				if categoryFilter != "" && ew.Analysis.Category != categoryFilter {
					continue
				}
				entryDate := ew.Entry.Timestamp
				entryDate = journal.TruncateTimestamp(entryDate, journal.DateDisplayLen)
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

	tools.Register(&tools.Tool{
		Name:        "summarize_daily_activities",
		Description: "Summarize a single day's journal entries into themes (by tags, entities, and existing analysis summaries). Use when the user asks 'what did I do on X?', 'summary of yesterday', or 'how was my day on ...'. Limitation: date must be YYYY-MM-DD or a supported natural expression (e.g. yesterday, today, last Monday).",
		Category:    "journal",
		Params: []tools.Param{
			tools.RequiredStringParam("date", "Date to summarize: YYYY-MM-DD or natural language (e.g. yesterday, today, last Monday)"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			dateArg, ok := args.RequiredString("date")
			if !ok {
				return tools.MissingParam("date")
			}
			startStr, endStr, err := resolveToolDateRange(dateArg, dateArg)
			if err != nil {
				return tools.Fail("Invalid date: %v", err)
			}
			withAnalyses, err := journal.GetEntriesWithAnalysisByDateRange(ctx, startStr, endStr, 100)
			if err != nil {
				return tools.Fail("Error fetching entries: %v", err)
			}
			if len(withAnalyses) == 0 {
				return tools.OK("No entries found for %s.", startStr)
			}
			var summaries []string
			tagCount := make(map[string]int)
			entityLines := make(map[string]string) // name -> "Type: Status"
			for _, ew := range withAnalyses {
				if ew.Analysis != nil {
					if s := strings.TrimSpace(ew.Analysis.Summary); s != "" {
						ts := journal.TruncateTimestamp(ew.Entry.Timestamp, journal.DateTimeDisplayLen)
						summaries = append(summaries, fmt.Sprintf("[%s] %s", ts, s))
					}
					for _, t := range ew.Analysis.Tags {
						tagCount[strings.TrimSpace(t)]++
					}
					for _, e := range ew.Analysis.Entities {
						key := e.Name + "|" + e.Type
						entityLines[key] = e.Type + ": " + e.Status
					}
				}
			}
			var out []string
			out = append(out, fmt.Sprintf("Summary for %s (%d entries):", startStr, len(withAnalyses)))
			out = append(out, "")
			out = append(out, "By entry:")
			for _, s := range summaries {
				out = append(out, "  "+s)
			}
			if len(tagCount) > 0 {
				out = append(out, "")
				out = append(out, "Themes (tags):")
				for tag, n := range tagCount {
					out = append(out, fmt.Sprintf("  - %s (%d)", tag, n))
				}
			}
			if len(entityLines) > 0 {
				out = append(out, "")
				out = append(out, "Entities mentioned:")
				for key, status := range entityLines {
					name := key
					if idx := strings.Index(key, "|"); idx >= 0 {
						name = key[:idx]
					}
					out = append(out, fmt.Sprintf("  - %s (%s)", name, status))
				}
			}
			return tools.OK("%s", strings.Join(out, "\n"))
		},
	})
}
