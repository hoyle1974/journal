package impl

import (
	"context"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot"
	"github.com/jackstrohm/jot/tools"
)

func init() {
	registerWebTools()
}

func registerWebTools() {
	tools.Register(&tools.Tool{
		Name:        "fetch_url",
		Description: "Fetch and extract text content from a web page URL. Returns the main text content, stripped of HTML.",
		Category:    "web",
		DocURL:      "https://github.com/go-shiori/go-readability",
		Params: []tools.Param{
			tools.RequiredStringParam("url", "The URL to fetch"),
			tools.IntParam("max_length", "Maximum characters to return (default 5000)", false, 5000),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			urlStr, ok := args.RequiredString("url")
			if !ok {
				return tools.MissingParam("url")
			}
			maxLength := args.Int("max_length", 5000)
			content, err := jot.FetchURLContent(urlStr, maxLength)
			if err != nil {
				return tools.Fail("Fetch error: %v", err)
			}
			return tools.OK("%s", content)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "define_word",
		Description: "Look up the definition of a word using a dictionary.",
		Category:    "web",
		Params: []tools.Param{
			tools.RequiredStringParam("word", "The word to define"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			word, ok := args.RequiredString("word")
			if !ok {
				return tools.MissingParam("word")
			}
			definition, err := jot.LookupWord(word)
			if err != nil {
				return tools.Fail("Definition error: %v", err)
			}
			return tools.OK("%s", definition)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "wikipedia",
		Description: "Search Wikipedia and get article summaries. Use for factual information, definitions, historical facts, biographies, etc.",
		Category:    "web",
		Params: []tools.Param{
			tools.RequiredStringParam("query", "The topic to search for on Wikipedia"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			query, ok := args.RequiredString("query")
			if !ok {
				return tools.MissingParam("query")
			}
			result, err := jot.SearchWikipedia(query)
			if err != nil {
				return tools.Fail("Wikipedia error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "web_search",
		Description: "Search for recent news articles. Use this for current events, recent news about people/companies/topics, or anything that happened recently. Returns headlines and links from news sources.",
		Category:    "web",
		Params: []tools.Param{
			tools.RequiredStringParam("query", "The search query (e.g., 'Keanu Reeves', 'Apple earnings', 'climate summit')"),
			{
				Name:        "num_results",
				Description: "Number of results to return (default 5, max 10)",
				Type:        genai.TypeInteger,
				Required:    false,
				Default:     5,
			},
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			query, ok := args.RequiredString("query")
			if !ok {
				return tools.MissingParam("query")
			}
			numResults := args.IntBounded("num_results", 5, 1, 10)
			result, err := jot.WebSearch(query, numResults)
			if err != nil {
				return tools.Fail("Web search error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "bookmark",
		Description: "Save, search, or list bookmarked URLs with optional tags and notes.",
		Category:    "web",
		Params: []tools.Param{
			tools.EnumParam("action", "Action: 'save', 'search', 'list', 'delete'", true, []string{"save", "search", "list", "delete"}),
			tools.OptionalStringParam("url", "The URL to bookmark (for save action)"),
			tools.OptionalStringParam("title", "Title/name for the bookmark (for save action)"),
			tools.OptionalStringParam("tags", "Comma-separated tags (for save/search)"),
			tools.OptionalStringParam("query", "Search query for finding bookmarks (for search action)"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			action, ok := args.RequiredString("action")
			if !ok {
				return tools.MissingParam("action")
			}
			bookmarkURL := args.String("url", "")
			title := args.String("title", "")
			tags := args.String("tags", "")
			query := args.String("query", "")
			result, err := jot.HandleBookmark(ctx, action, bookmarkURL, title, tags, query)
			if err != nil {
				return tools.Fail("Bookmark error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "countdown",
		Description: "Manage countdowns to events. Create named countdowns, check days remaining, or list all countdowns.",
		Category:    "web",
		Params: []tools.Param{
			tools.EnumParam("action", "Action: 'create', 'check', 'list', 'delete'", true, []string{"create", "check", "list", "delete"}),
			tools.OptionalStringParam("name", "Name of the countdown event (for create/check/delete)"),
			tools.OptionalStringParam("date", "Target date for the event (YYYY-MM-DD, for create action)"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			action, ok := args.RequiredString("action")
			if !ok {
				return tools.MissingParam("action")
			}
			name := args.String("name", "")
			dateStr := args.String("date", "")
			result, err := jot.HandleCountdown(ctx, action, name, dateStr)
			if err != nil {
				return tools.Fail("Countdown error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})
}
