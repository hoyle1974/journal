package jot

import (
	"github.com/jackstrohm/jot/pkg/utils"
)

// FetchURLContent fetches and extracts text from a URL. Re-exported from utils.
func FetchURLContent(pageURL string, maxLength int) (string, error) {
	return utils.FetchURLContent(pageURL, maxLength)
}

// AnalyzeText returns statistics about a text. Re-exported from utils.
func AnalyzeText(text string) string {
	return utils.AnalyzeText(text)
}

// SearchWikipedia searches Wikipedia and returns article summary. Re-exported from utils.
func SearchWikipedia(query string) (string, error) {
	return utils.SearchWikipedia(query)
}

// SearchWikipediaFallback uses the search API when direct lookup fails. Re-exported from utils.
func SearchWikipediaFallback(query string) (string, error) {
	return utils.SearchWikipediaFallback(query)
}

// WebSearch performs a web search using Google News RSS. Re-exported from utils.
func WebSearch(query string, numResults int) (string, error) {
	return utils.WebSearch(query, numResults)
}
