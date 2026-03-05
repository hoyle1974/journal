package impl

import (
	"context"

	"github.com/jackstrohm/jot"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
)

func init() {
	registerQueryTools()
}

func registerQueryTools() {
	tools.Register(&tools.Tool{
		Name:        "get_recent_queries",
		Description: "Get recent queries/questions and their answers from the query history.",
		Category:    "query",
		Params:      []tools.Param{tools.CountParam()},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			count := args.IntBounded("count", 10, 1, 50)
			queries, err := jot.GetRecentQueries(ctx, count)
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
		Params: []tools.Param{
			tools.RequiredStringParam("query", "Search term to find in questions or answers"),
			tools.LimitParam(10, 50),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			query, ok := args.RequiredString("query")
			if !ok {
				return tools.MissingParam("query")
			}
			limit := args.IntBounded("limit", 10, 1, 50)
			queries, err := jot.SearchQueries(ctx, query, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(queries) == 0 {
				return tools.OK("No queries matching '%s' found.", query)
			}
			result := formatQueriesForContext(queries)
			return tools.OK("Found %d queries matching '%s':\n%s", len(queries), query, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_queries_by_date",
		Description: "Get queries within a date range. Accepts YYYY-MM-DD or natural language (e.g. yesterday, last week, since Tuesday).",
		Category:    "query",
		Params: []tools.Param{
			tools.RequiredStringParam("start_date", "Start date (YYYY-MM-DD or natural: yesterday, last week, since Tuesday)"),
			tools.RequiredStringParam("end_date", "End date (YYYY-MM-DD or natural: today, yesterday)"),
			tools.LimitParam(20, 50),
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
			startStr, endStr, err := utils.ResolveDateRange(startDate, endDate)
			if err != nil {
				return tools.Fail("Date range error: %v", err)
			}
			limit := args.IntBounded("limit", 20, 1, 50)
			queries, err := jot.GetQueriesByDateRange(ctx, startStr, endStr, limit)
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
