package utils

import (
	"bytes"
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
)

// FetchURLContent fetches and extracts text from a URL.
func FetchURLContent(pageURL string, maxLength int) (string, error) {
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
		text = TruncateToMaxBytes(text, maxLength) + "...[truncated]"
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

// AnalyzeText returns statistics about a text.
func AnalyzeText(text string) string {
	charCount := len(text)
	charCountNoSpaces := len(strings.ReplaceAll(text, " ", ""))

	words := strings.Fields(text)
	wordCount := len(words)

	sentenceRegex := regexp.MustCompile(`[.!?]+`)
	sentences := sentenceRegex.FindAllString(text, -1)
	sentenceCount := len(sentences)
	if sentenceCount == 0 && wordCount > 0 {
		sentenceCount = 1
	}

	readingMinutes := float64(wordCount) / 200.0

	paragraphs := strings.Split(text, "\n\n")
	paraCount := 0
	for _, p := range paragraphs {
		if strings.TrimSpace(p) != "" {
			paraCount++
		}
	}

	return fmt.Sprintf("Text Statistics:\n- Characters: %d (without spaces: %d)\n- Words: %d\n- Sentences: %d\n- Paragraphs: %d\n- Reading time: %.1f minutes",
		charCount, charCountNoSpaces, wordCount, sentenceCount, paraCount, readingMinutes)
}

// SearchWikipedia searches Wikipedia and returns article summary.
func SearchWikipedia(query string) (string, error) {
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
		return SearchWikipediaFallback(query)
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

// SearchWikipediaFallback uses the search API when direct lookup fails.
func SearchWikipediaFallback(query string) (string, error) {
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
	return SearchWikipedia(title)
}

// WebSearch performs a web search using Google News RSS feed.
func WebSearch(query string, numResults int) (string, error) {
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

	type RSSItem struct {
		Title   string `xml:"title"`
		Link    string `xml:"link"`
		PubDate string `xml:"pubDate"`
		Source  string `xml:"source"`
	}
	type RSSChannel struct {
		Items []RSSItem `xml:"item"`
	}
	type RSS struct {
		Channel RSSChannel `xml:"channel"`
	}

	var rss RSS
	if err := xml.Unmarshal(body, &rss); err != nil {
		return "", fmt.Errorf("failed to parse RSS: %v", err)
	}

	if len(rss.Channel.Items) == 0 {
		return fmt.Sprintf("No news results found for '%s'", query), nil
	}

	var resultLines []string
	resultLines = append(resultLines, fmt.Sprintf("Recent news for '%s':\n", query))

	count := numResults
	if count > len(rss.Channel.Items) {
		count = len(rss.Channel.Items)
	}

	for i := 0; i < count; i++ {
		item := rss.Channel.Items[i]
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
