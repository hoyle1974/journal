package impl

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
)

type getRecentEntriesArgs struct {
	Count    int  `json:"count" description:"Number of items to retrieve (default 10, max 50)" default:"10"`
	HasImage bool `json:"has_image" description:"Set to true to filter results strictly to journal entries that contain an attached image."`
}

type getOldestEntriesArgs struct {
	Count int `json:"count" description:"Number of items to retrieve (default 10, max 50)" default:"10"`
}

type getEntriesByDateRangeArgs struct {
	StartDate string `json:"start_date" description:"Start date (YYYY-MM-DD or natural: yesterday, last week, this morning, since Tuesday)" required:"true"`
	EndDate   string `json:"end_date" description:"End date (YYYY-MM-DD or natural: today, yesterday, last week)" required:"true"`
	Limit     int    `json:"limit" description:"Maximum number of results (default 50, max 200)" default:"50"`
	HasImage  bool   `json:"has_image" description:"Set to true to filter results strictly to journal entries that contain an attached image."`
}

type searchEntriesArgs struct {
	Query    string `json:"query" description:"The keyword or phrase to search for" required:"true"`
	Category string `json:"category" description:"Filter by category: work, personal, health, finance, logistics (requires entries to have been analyzed)"`
	Limit    int    `json:"limit" description:"Maximum number of results (default 20, max 50)" default:"20"`
	HasImage bool   `json:"has_image" description:"Set to true to filter results strictly to journal entries that contain an attached image."`
}

type countEntriesArgs struct {
	StartDate string `json:"start_date" description:"Start date (YYYY-MM-DD, optional)"`
	EndDate   string `json:"end_date" description:"End date (YYYY-MM-DD, optional)"`
}

type getEntriesBySourceArgs struct {
	Source string `json:"source" description:"The source to filter by (e.g., 'cli', 'sms', 'web')" required:"true"`
	Count  int    `json:"count" description:"Number of items to retrieve (default 10, max 50)" default:"10"`
}

type queryActivityHistoryArgs struct {
	Topic     string `json:"topic" description:"The topic or keyword to summarize (e.g. 'migraines', 'work stress', 'jot app')" required:"true"`
	Timeframe string `json:"timeframe" description:"Optional timeframe: 'last 6 months', 'last 30 days', 'this year', or leave empty for all matching entries (up to 100)"`
}

type queryEntitiesArgs struct {
	StartDate  string `json:"start_date" description:"Start date (YYYY-MM-DD or natural: last week, yesterday). Default: 30 days ago."`
	EndDate    string `json:"end_date" description:"End date (YYYY-MM-DD or natural: today). Default: today."`
	EntityType string `json:"entity_type" description:"Filter by type: person, project, event, place"`
	Name       string `json:"name" description:"Filter by entity name (substring match, case-insensitive)"`
	Status     string `json:"status" description:"Filter by status: Planned, In-Progress, Stalled, Completed. Omit to include all."`
	Category   string `json:"category" description:"Filter by entry category: work, personal, health, finance, logistics"`
	Limit     int    `json:"limit" description:"Maximum number of results (default 50, max 200)" default:"50"`
}

type summarizeDailyActivitiesArgs struct {
	Date string `json:"date" description:"Date to summarize: YYYY-MM-DD or natural language (e.g. yesterday, today, last Monday)" required:"true"`
}

func journalClamp(val, def, min, max int) int {
	if val == 0 {
		val = def
	}
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func init() {
	registerJournalTools()
}

func registerJournalTools() {
	tools.Register(&tools.Tool{
		Name:        "get_recent_entries",
		Description: "Get the most recent journal entries (newest first). First result is the latest in time; last result is the oldest in the returned set. Use for 'recent' or 'latest' — NOT for 'oldest' or 'earliest'. Set has_image=true to return only entries that contain an attached image.",
		Category:    "journal",
		Args:        &getRecentEntriesArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*getRecentEntriesArgs)
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			count := journalClamp(a.Count, 10, 1, 50)
			limit := count
			if a.HasImage {
				limit = count * 5
				if limit > 100 {
					limit = 100
				}
			}
			entries, err := journal.GetEntries(ctx, client, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if a.HasImage {
				entries = filterEntriesWithImage(entries, count)
				if len(entries) == 0 {
					return tools.OK("No recent entries with an attached image found.")
				}
			}
			result := journal.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d recent entries:\n%s", len(entries), result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_oldest_entries",
		Description: "Get the chronologically oldest journal entries (earliest by timestamp). First result is the OLDEST entry; use this when the user asks for 'oldest', 'earliest', or 'first ever' entry or memory.",
		Category:    "journal",
		Args:        &getOldestEntriesArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*getOldestEntriesArgs)
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			count := journalClamp(a.Count, 10, 1, 50)
			entries, err := journal.GetEntriesAsc(ctx, client, count)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			result := journal.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d oldest entries (chronological order):\n%s", len(entries), result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_entries_by_date_range",
		Description: "Get journal entries within a date range (newest first within range). Accepts YYYY-MM-DD or natural language (e.g. 'yesterday', 'last week', 'this morning', 'since Tuesday'). Dates use server (UTC) calendar day. For 'oldest entry ever' use get_oldest_entries instead.",
		Category:    "journal",
		Args:        &getEntriesByDateRangeArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*getEntriesByDateRangeArgs)
			if a.StartDate == "" {
				return tools.MissingParam("start_date")
			}
			if a.EndDate == "" {
				return tools.MissingParam("end_date")
			}
			startStr, endStr, err := resolveToolDateRange(a.StartDate, a.EndDate)
			if err != nil {
				return tools.Fail("Date range error: %v", err)
			}
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			limit := journalClamp(a.Limit, 50, 1, 200)
			fetchLimit := limit
			if a.HasImage {
				fetchLimit = limit * 3
				if fetchLimit > 500 {
					fetchLimit = 500
				}
			}
			entries, err := journal.GetEntriesByDateRange(ctx, client, startStr, endStr, fetchLimit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if a.HasImage {
				entries = filterEntriesWithImage(entries, limit)
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
		Description: "Search journal entries for a keyword or phrase. Use semantic_search FIRST for factual questions. Optional category filters by analysis category (work, personal, health, finance, logistics). Set has_image=true to return only entries that contain an attached image. Limitation: when category is used, only the last calendar month of entries is searched.",
		Category:    "journal",
		Args:        &searchEntriesArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*searchEntriesArgs)
			if a.Query == "" {
				return tools.MissingParam("query")
			}
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			limit := journalClamp(a.Limit, 20, 1, 50)
			searchLimit := limit
			if a.HasImage {
				searchLimit = limit * 5
				if searchLimit > 100 {
					searchLimit = 100
				}
			}
			categoryFilter := strings.ToLower(strings.TrimSpace(a.Category))
			var entries []journal.Entry
			if categoryFilter != "" {
				startStr, endStr, err := resolveToolDateRange("last month", "today")
				if err != nil {
					return tools.Fail("Date range error: %v", err)
				}
				withAnalyses, err := journal.GetEntriesWithAnalysisByDateRange(ctx, client, startStr, endStr, 200)
				if err != nil {
					return tools.Fail("Error: %v", err)
				}
				queryLower := strings.ToLower(a.Query)
				for _, ew := range withAnalyses {
					if ew.Analysis == nil || ew.Analysis.Category != categoryFilter {
						continue
					}
					if !strings.Contains(strings.ToLower(ew.Entry.Content), queryLower) {
						continue
					}
					entries = append(entries, ew.Entry)
					if len(entries) >= searchLimit {
						break
					}
				}
			} else {
				entries, err = journal.SearchEntries(ctx, client, a.Query, searchLimit)
				if err != nil {
					return tools.Fail("Error: %v", err)
				}
			}
			if a.HasImage {
				entries = filterEntriesWithImage(entries, limit)
			}
			if len(entries) == 0 {
				return tools.OK("No entries matching '%s' found.", a.Query)
			}
			result := journal.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d entries matching '%s':\n%s", len(entries), a.Query, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "count_entries",
		Description: "Count journal entries within a date range.",
		Category:    "journal",
		Args:        &countEntriesArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*countEntriesArgs)
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			var startDate, endDate *string
			if a.StartDate != "" {
				startDate = &a.StartDate
			}
			if a.EndDate != "" {
				endDate = &a.EndDate
			}
			count, err := journal.CountEntries(ctx, client, startDate, endDate)
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
		Args:        &tools.NoArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			entries, err := journal.GetEntries(ctx, client, 100)
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
		Args:        &getEntriesBySourceArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*getEntriesBySourceArgs)
			if a.Source == "" {
				return tools.MissingParam("source")
			}
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			count := journalClamp(a.Count, 10, 1, 50)
			entries, err := journal.GetEntriesBySource(ctx, client, a.Source, count)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(entries) == 0 {
				return tools.OK("No entries found from source '%s'.", a.Source)
			}
			result := journal.FormatEntriesForContext(entries, 10000)
			return tools.OK("Found %d entries from '%s':\n%s", len(entries), a.Source, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "query_activity_history",
		Description: "Get a thematic, chronological summary of journal entries about a topic (e.g. migraines, work stress, a project). Use when the user asks 'how have my X been?', 'what's been going on with Y?', or for a distilled timeline. Fetches a larger batch and uses an LLM to produce a concise summary. Limitation: optional timeframe must use supported expressions only—YYYY-MM-DD, 'last month', 'last week', 'yesterday', 'today'; phrases like 'last 6 months' or 'last 30 days' are not supported and will error.",
		Category:    "journal",
		Args:        &queryActivityHistoryArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*queryActivityHistoryArgs)
			if a.Topic == "" {
				return tools.MissingParam("topic")
			}
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			var entries []journal.Entry
			if a.Timeframe != "" {
				startStr, endStr, err := resolveToolDateRange(a.Timeframe, "today")
				if err != nil {
					return tools.Fail("Invalid timeframe: %v", err)
				}
				byDate, err := journal.GetEntriesByDateRange(ctx, client, startStr, endStr, 200)
				if err != nil {
					return tools.Fail("Error fetching entries: %v", err)
				}
				topicLower := strings.ToLower(a.Topic)
				for _, e := range byDate {
					if strings.Contains(strings.ToLower(e.Content), topicLower) {
						entries = append(entries, e)
					}
				}
			} else {
				entries, err = journal.SearchEntries(ctx, client, a.Topic, 100)
				if err != nil {
					return tools.Fail("Error searching entries: %v", err)
				}
			}
			if len(entries) == 0 {
				return tools.OK("No entries found for topic '%s'.", a.Topic)
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Timestamp < entries[j].Timestamp
			})
			entriesText := journal.FormatEntriesForContext(entries, 12000)
			if env == nil || env.Config() == nil {
				return tools.Fail("App not available for summarization")
			}
			userPrompt, err := prompts.BuildActivityHistory(prompts.ActivityHistoryData{
				Topic:       a.Topic,
				Timeframe:   a.Timeframe,
				EntriesText: utils.WrapAsUserData(utils.SanitizePrompt(entriesText)),
			})
			if err != nil {
				return tools.Fail("Failed to build activity history prompt: %v", err)
			}
			systemPrompt := prompts.DataSafety()
			summary, err := infra.GenerateContentSimple(ctx, env, systemPrompt, userPrompt, env.Config(), &infra.GenConfig{MaxOutputTokens: 1024})
			if err != nil {
				return tools.Fail("Summarization failed: %v", err)
			}
			return tools.OK("Activity history for '%s'%s:\n\n%s", a.Topic, func() string {
				if a.Timeframe != "" {
					return " (" + a.Timeframe + ")"
				}
				return ""
			}(), summary)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "query_entities",
		Description: "Query extracted entities (person, project, event, place) by type, name, and status. Use when the user asks 'what do I have left for X', 'status of the party', or tasks for a specific project/event. Status values: Planned, In-Progress, Stalled, Completed. Filter by status to find incomplete work (e.g. exclude Completed).",
		Category:    "journal",
		Args:        &queryEntitiesArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*queryEntitiesArgs)
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			startDate := a.StartDate
			if startDate == "" {
				startDate = "30 days ago"
			}
			endDate := a.EndDate
			if endDate == "" {
				endDate = "today"
			}
			startStr, endStr, err := resolveToolDateRange(startDate, endDate)
			if err != nil {
				return tools.Fail("Date range error: %v", err)
			}
			limit := journalClamp(a.Limit, 50, 1, 200)
			withAnalyses, err := journal.GetEntriesWithAnalysisByDateRange(ctx, client, startStr, endStr, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			entityType := strings.ToLower(strings.TrimSpace(a.EntityType))
			nameSubstr := strings.ToLower(strings.TrimSpace(a.Name))
			statusFilter := strings.TrimSpace(a.Status)
			categoryFilter := strings.ToLower(strings.TrimSpace(a.Category))

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
		Args:        &summarizeDailyActivitiesArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*summarizeDailyActivitiesArgs)
			if a.Date == "" {
				return tools.MissingParam("date")
			}
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			startStr, endStr, err := resolveToolDateRange(a.Date, a.Date)
			if err != nil {
				return tools.Fail("Invalid date: %v", err)
			}
			withAnalyses, err := journal.GetEntriesWithAnalysisByDateRange(ctx, client, startStr, endStr, 100)
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
