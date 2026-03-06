package impl

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/pkg/utils"
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
			content, err := fetchURLContent(urlStr, maxLength)
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
			definition, err := lookupWord(word)
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
			result, err := searchWikipedia(query)
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
			result, err := webSearch(query, numResults)
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
			result, err := HandleBookmark(ctx, action, bookmarkURL, title, tags, query)
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
			result, err := HandleCountdown(ctx, action, name, dateStr)
			if err != nil {
				return tools.Fail("Countdown error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})
}

// fetchURLContent fetches and extracts text from a URL (tool: fetch_url).
func fetchURLContent(pageURL string, maxLength int) (string, error) {
	if !strings.HasPrefix(pageURL, "http://") && !strings.HasPrefix(pageURL, "https://") {
		pageURL = "https://" + pageURL
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(pageURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", err
	}

	parsedURL, _ := url.Parse(pageURL)
	article, err := readability.FromReader(bytes.NewReader(body), parsedURL)
	var text string
	if err == nil && strings.TrimSpace(article.TextContent) != "" {
		text = article.TextContent
		if article.Title != "" {
			text = article.Title + "\n\n" + text
		}
	} else {
		text = stripHTMLTags(string(body))
	}
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	if len(text) > maxLength {
		text = utils.TruncateToMaxBytes(text, maxLength) + "...[truncated]"
	}

	return fmt.Sprintf("Content from %s:\n\n%s", pageURL, text), nil
}

func stripHTMLTags(html string) string {
	scriptRegex := regexp.MustCompile(`(?is)<script.*?</script>`)
	styleRegex := regexp.MustCompile(`(?is)<style.*?</style>`)
	html = scriptRegex.ReplaceAllString(html, "")
	html = styleRegex.ReplaceAllString(html, "")

	tagRegex := regexp.MustCompile(`<[^>]*>`)
	text := tagRegex.ReplaceAllString(html, " ")

	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")

	return text
}

// lookupWord fetches word definition from Free Dictionary API (tool: define_word).
func lookupWord(word string) (string, error) {
	word = strings.ToLower(strings.TrimSpace(word))
	apiURL := fmt.Sprintf("https://api.dictionaryapi.dev/api/v2/entries/en/%s", word)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return fmt.Sprintf("No definition found for '%s'", word), nil
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("dictionary API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var entries []struct {
		Word     string `json:"word"`
		Phonetic string `json:"phonetic"`
		Meanings []struct {
			PartOfSpeech string `json:"partOfSpeech"`
			Definitions  []struct {
				Definition string `json:"definition"`
				Example    string `json:"example"`
			} `json:"definitions"`
		} `json:"meanings"`
	}

	if err := json.Unmarshal(body, &entries); err != nil {
		return "", fmt.Errorf("failed to parse dictionary response: %v", err)
	}

	if len(entries) == 0 {
		return fmt.Sprintf("No definition found for '%s'", word), nil
	}

	var result []string
	entry := entries[0]
	result = append(result, fmt.Sprintf("**%s**", entry.Word))
	if entry.Phonetic != "" {
		result = append(result, fmt.Sprintf("Pronunciation: %s", entry.Phonetic))
	}
	result = append(result, "")

	for _, meaning := range entry.Meanings {
		result = append(result, fmt.Sprintf("_%s_", meaning.PartOfSpeech))
		for i, def := range meaning.Definitions {
			if i >= 3 {
				break
			}
			result = append(result, fmt.Sprintf("%d. %s", i+1, def.Definition))
			if def.Example != "" {
				result = append(result, fmt.Sprintf("   Example: \"%s\"", def.Example))
			}
		}
		result = append(result, "")
	}

	return strings.Join(result, "\n"), nil
}

// searchWikipedia searches Wikipedia and returns article summary (tool: wikipedia).
func searchWikipedia(query string) (string, error) {
	searchURL := fmt.Sprintf("https://en.wikipedia.org/api/rest_v1/page/summary/%s", url.PathEscape(query))

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "JotPersonalAssistant/1.0 (https://github.com/jackstrohm/jot)")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return searchWikipediaFallback(query)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Wikipedia API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var article struct {
		Title       string `json:"title"`
		Extract     string `json:"extract"`
		Description string `json:"description"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
	}

	if err := json.Unmarshal(body, &article); err != nil {
		return "", fmt.Errorf("failed to parse Wikipedia response: %v", err)
	}

	if article.Extract == "" {
		return fmt.Sprintf("No Wikipedia article found for '%s'", query), nil
	}

	var result []string
	result = append(result, fmt.Sprintf("**%s**", article.Title))
	if article.Description != "" {
		result = append(result, fmt.Sprintf("_%s_", article.Description))
	}
	result = append(result, "")
	result = append(result, article.Extract)
	if article.ContentURLs.Desktop.Page != "" {
		result = append(result, "")
		result = append(result, fmt.Sprintf("Read more: %s", article.ContentURLs.Desktop.Page))
	}

	return strings.Join(result, "\n"), nil
}

func searchWikipediaFallback(query string) (string, error) {
	searchURL := fmt.Sprintf("https://en.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json&srlimit=1", url.QueryEscape(query))

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "JotPersonalAssistant/1.0 (https://github.com/jackstrohm/jot)")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var searchResult struct {
		Query struct {
			Search []struct {
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"search"`
		} `json:"query"`
	}

	if err := json.Unmarshal(body, &searchResult); err != nil {
		return "", fmt.Errorf("failed to parse search response: %v", err)
	}

	if len(searchResult.Query.Search) == 0 {
		return fmt.Sprintf("No Wikipedia articles found for '%s'", query), nil
	}

	title := searchResult.Query.Search[0].Title
	return searchWikipedia(title)
}

// webSearch performs a web search using Google News RSS feed (tool: web_search).
func webSearch(query string, numResults int) (string, error) {
	searchURL := fmt.Sprintf("https://news.google.com/rss/search?q=%s&hl=en-US&gl=US&ceid=US:en", url.QueryEscape(query))

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "JotPersonalAssistant/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("search returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	type rssItem struct {
		Title   string `xml:"title"`
		Link    string `xml:"link"`
		PubDate string `xml:"pubDate"`
		Source  string `xml:"source"`
	}
	type rssChannel struct {
		Items []rssItem `xml:"item"`
	}
	type rss struct {
		Channel rssChannel `xml:"channel"`
	}

	var r rss
	if err := xml.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("failed to parse RSS: %v", err)
	}

	if len(r.Channel.Items) == 0 {
		return fmt.Sprintf("No news results found for '%s'", query), nil
	}

	var resultLines []string
	resultLines = append(resultLines, fmt.Sprintf("Recent news for '%s':\n", query))

	count := numResults
	if count > len(r.Channel.Items) {
		count = len(r.Channel.Items)
	}

	for i := 0; i < count; i++ {
		item := r.Channel.Items[i]
		title := item.Title
		pubDate := item.PubDate
		if t, err := time.Parse(time.RFC1123, pubDate); err == nil {
			pubDate = t.Format("Jan 2, 2006")
		} else if t, err := time.Parse(time.RFC1123Z, pubDate); err == nil {
			pubDate = t.Format("Jan 2, 2006")
		}

		resultLines = append(resultLines, fmt.Sprintf("%d. %s\n   Date: %s\n   %s\n", i+1, title, pubDate, item.Link))
	}

	return strings.Join(resultLines, "\n"), nil
}
