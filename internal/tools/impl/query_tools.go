package impl

import (
	"context"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/tools"
)

type getRecentQueriesArgs struct {
	Count int `json:"count" description:"Number of items to retrieve (default 10, max 50)" default:"10"`
}

type searchQueriesArgs struct {
	Query string `json:"query" description:"Search term to find in questions or answers" required:"true"`
	Limit int    `json:"limit" description:"Maximum number of results to return (default 10, max 50)" default:"10"`
}

type getQueriesByDateArgs struct {
	StartDate string `json:"start_date" description:"Start date (YYYY-MM-DD or natural: yesterday, last week, since Tuesday)" required:"true"`
	EndDate   string `json:"end_date" description:"End date (YYYY-MM-DD or natural: today, yesterday)" required:"true"`
	Limit     int    `json:"limit" description:"Maximum number of results (default 20, max 50)" default:"20"`
}

func init() {
	registerQueryTools()
}

func registerQueryTools() {
	tools.Register(&tools.Tool{
		Name:        "get_recent_queries",
		Description: "Get recent queries/questions and their answers from the query history.",
		Category:    "query",
		Args:        &getRecentQueriesArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*getRecentQueriesArgs)
			count := clampInt(a.Count, 10, 1, 50)
			queries, err := env.MemoryStore().GetRecentQueries(ctx, count)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(queries) == 0 {
				return tools.OK("No queries found.")
			}
			result := formatQueriesForContext(queries)
			return tools.OK("Found %d recent queries:\n%s", len(queries), result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "search_queries",
		Description: "Search query history for matching questions or answers.",
		Category:    "query",
		Args:        &searchQueriesArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*searchQueriesArgs)
			if a.Query == "" {
				return tools.MissingParam("query")
			}
			limit := clampInt(a.Limit, 10, 1, 50)
			queries, err := env.MemoryStore().SearchQueries(ctx, a.Query, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(queries) == 0 {
				return tools.OK("No queries matching '%s' found.", a.Query)
			}
			result := formatQueriesForContext(queries)
			return tools.OK("Found %d queries matching '%s':\n%s", len(queries), a.Query, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_queries_by_date",
		Description: "Get queries within a date range. Accepts YYYY-MM-DD or natural language (e.g. yesterday, last week, since Tuesday).",
		Category:    "query",
		Args:        &getQueriesByDateArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*getQueriesByDateArgs)
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
			limit := clampInt(a.Limit, 20, 1, 50)
			queries, err := env.MemoryStore().GetQueriesByDateRange(ctx, startStr, endStr, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(queries) == 0 {
				return tools.OK("No queries found between %s and %s.", startStr, endStr)
			}
			result := formatQueriesForContext(queries)
			return tools.OK("Found %d queries from %s to %s:\n%s", len(queries), startStr, endStr, result)
		},
	})
}
