package jot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// GenerateRandom generates random values
func GenerateRandom(randType string, minVal, maxVal int, choices string) string {
	switch strings.ToLower(randType) {
	case "number":
		if maxVal <= minVal {
			maxVal = minVal + 100
		}
		n := rand.Intn(maxVal-minVal+1) + minVal
		return fmt.Sprintf("Random number (%d-%d): %d", minVal, maxVal, n)

	case "uuid":
		return fmt.Sprintf("Random UUID: %s", uuid.New().String())

	case "pick":
		if choices == "" {
			return "Error: 'choices' parameter required for pick"
		}
		items := strings.Split(choices, ",")
		for i := range items {
			items[i] = strings.TrimSpace(items[i])
		}
		pick := items[rand.Intn(len(items))]
		return fmt.Sprintf("Picked: %s (from %d choices)", pick, len(items))

	case "coin":
		if rand.Intn(2) == 0 {
			return "Coin flip: Heads"
		}
		return "Coin flip: Tails"

	case "dice", "die":
		n := rand.Intn(6) + 1
		return fmt.Sprintf("Dice roll: %d", n)

	default:
		return fmt.Sprintf("Unknown random type: %s (use: number, uuid, pick, coin, dice)", randType)
	}
}

// HandleCountdown manages countdown events
func HandleCountdown(ctx context.Context, action, name, dateStr string) (string, error) {
	switch strings.ToLower(action) {
	case "create":
		if name == "" || dateStr == "" {
			return "", fmt.Errorf("name and date required for create")
		}
		targetDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return "", fmt.Errorf("invalid date format (use YYYY-MM-DD): %v", err)
		}
		metadata := fmt.Sprintf(`{"target_date": "%s"}`, dateStr)
		id, err := UpsertKnowledge(ctx, fmt.Sprintf("Countdown: %s", name), "countdown", metadata)
		if err != nil {
			return "", err
		}
		daysUntil := int(time.Until(targetDate).Hours() / 24)
		return fmt.Sprintf("Countdown '%s' created for %s (%d days away). ID: %s", name, dateStr, daysUntil, id), nil

	case "check":
		if name == "" {
			return "", fmt.Errorf("name required for check")
		}
		queryVec, err := GenerateEmbedding(ctx, fmt.Sprintf("Countdown: %s", name))
		if err != nil {
			return "", err
		}
		nodes, err := QuerySimilarNodes(ctx, queryVec, 5)
		if err != nil {
			return "", err
		}
		for _, node := range nodes {
			if node.NodeType == "countdown" && strings.Contains(strings.ToLower(node.Content), strings.ToLower(name)) {
				var meta map[string]string
				if err := json.Unmarshal([]byte(node.Metadata), &meta); err == nil {
					if targetStr, ok := meta["target_date"]; ok {
						targetDate, _ := time.Parse("2006-01-02", targetStr)
						daysUntil := int(time.Until(targetDate).Hours() / 24)
						if daysUntil < 0 {
							return fmt.Sprintf("'%s' was %d days ago (%s)", name, -daysUntil, targetStr), nil
						}
						return fmt.Sprintf("%d days until '%s' (%s)", daysUntil, name, targetStr), nil
					}
				}
			}
		}
		return fmt.Sprintf("Countdown '%s' not found", name), nil

	case "list":
		queryVec, err := GenerateEmbedding(ctx, "Countdown event")
		if err != nil {
			return "", err
		}
		nodes, err := QuerySimilarNodes(ctx, queryVec, 20)
		if err != nil {
			return "", err
		}
		var countdowns []string
		for _, node := range nodes {
			if node.NodeType == "countdown" {
				var meta map[string]string
				if err := json.Unmarshal([]byte(node.Metadata), &meta); err == nil {
					if targetStr, ok := meta["target_date"]; ok {
						targetDate, _ := time.Parse("2006-01-02", targetStr)
						daysUntil := int(time.Until(targetDate).Hours() / 24)
						name := strings.TrimPrefix(node.Content, "Countdown: ")
						if daysUntil < 0 {
							countdowns = append(countdowns, fmt.Sprintf("- %s: %d days ago (%s)", name, -daysUntil, targetStr))
						} else {
							countdowns = append(countdowns, fmt.Sprintf("- %s: %d days (%s)", name, daysUntil, targetStr))
						}
					}
				}
			}
		}
		if len(countdowns) == 0 {
			return "No countdowns found.", nil
		}
		return fmt.Sprintf("Countdowns:\n%s", strings.Join(countdowns, "\n")), nil

	case "delete":
		return "", fmt.Errorf("delete not yet implemented - countdowns are stored in knowledge graph")

	default:
		return "", fmt.Errorf("unknown action: %s (use: create, check, list)", action)
	}
}

// LookupWord fetches word definition from Free Dictionary API
func LookupWord(word string) (string, error) {
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

// EncodeDecodeText performs encoding/decoding operations
func EncodeDecodeText(operation, text string) (string, error) {
	switch strings.ToLower(operation) {
	case "base64_encode":
		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		return fmt.Sprintf("Base64 encoded:\n%s", encoded), nil

	case "base64_decode":
		decoded, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return "", fmt.Errorf("invalid base64: %v", err)
		}
		return fmt.Sprintf("Base64 decoded:\n%s", string(decoded)), nil

	case "url_encode":
		encoded := url.QueryEscape(text)
		return fmt.Sprintf("URL encoded:\n%s", encoded), nil

	case "url_decode":
		decoded, err := url.QueryUnescape(text)
		if err != nil {
			return "", fmt.Errorf("invalid URL encoding: %v", err)
		}
		return fmt.Sprintf("URL decoded:\n%s", decoded), nil

	case "json_format", "json_prettify":
		var data interface{}
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			return "", fmt.Errorf("invalid JSON: %v", err)
		}
		formatted, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Formatted JSON:\n%s", string(formatted)), nil

	case "json_minify":
		var data interface{}
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			return "", fmt.Errorf("invalid JSON: %v", err)
		}
		minified, err := json.Marshal(data)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Minified JSON:\n%s", string(minified)), nil

	default:
		return "", fmt.Errorf("unknown operation: %s (use: base64_encode, base64_decode, url_encode, url_decode, json_format, json_minify)", operation)
	}
}

// HandleBookmark manages bookmarks
func HandleBookmark(ctx context.Context, action, bookmarkURL, title, tags, query string) (string, error) {
	switch strings.ToLower(action) {
	case "save":
		if bookmarkURL == "" {
			return "", fmt.Errorf("url required for save")
		}
		if title == "" {
			title = bookmarkURL
		}
		metadata := map[string]interface{}{
			"url":  bookmarkURL,
			"tags": strings.Split(tags, ","),
		}
		metaJSON, _ := json.Marshal(metadata)
		id, err := UpsertKnowledge(ctx, fmt.Sprintf("Bookmark: %s", title), "bookmark", string(metaJSON))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Bookmark saved: %s\nURL: %s\nID: %s", title, bookmarkURL, id), nil

	case "search":
		searchQuery := query
		if searchQuery == "" && tags != "" {
			searchQuery = tags
		}
		if searchQuery == "" {
			return "", fmt.Errorf("query or tags required for search")
		}
		queryVec, err := GenerateEmbedding(ctx, fmt.Sprintf("Bookmark %s", searchQuery))
		if err != nil {
			return "", err
		}
		nodes, err := QuerySimilarNodes(ctx, queryVec, 10)
		if err != nil {
			return "", err
		}
		var bookmarks []string
		for _, node := range nodes {
			if node.NodeType == "bookmark" {
				var meta map[string]interface{}
				if err := json.Unmarshal([]byte(node.Metadata), &meta); err == nil {
					urlStr, _ := meta["url"].(string)
					title := strings.TrimPrefix(node.Content, "Bookmark: ")
					bookmarks = append(bookmarks, fmt.Sprintf("- %s\n  %s", title, urlStr))
				}
			}
		}
		if len(bookmarks) == 0 {
			return fmt.Sprintf("No bookmarks found matching '%s'", searchQuery), nil
		}
		return fmt.Sprintf("Bookmarks matching '%s':\n%s", searchQuery, strings.Join(bookmarks, "\n")), nil

	case "list":
		queryVec, err := GenerateEmbedding(ctx, "Bookmark")
		if err != nil {
			return "", err
		}
		nodes, err := QuerySimilarNodes(ctx, queryVec, 20)
		if err != nil {
			return "", err
		}
		var bookmarks []string
		for _, node := range nodes {
			if node.NodeType == "bookmark" {
				var meta map[string]interface{}
				if err := json.Unmarshal([]byte(node.Metadata), &meta); err == nil {
					urlStr, _ := meta["url"].(string)
					title := strings.TrimPrefix(node.Content, "Bookmark: ")
					bookmarks = append(bookmarks, fmt.Sprintf("- %s\n  %s", title, urlStr))
				}
			}
		}
		if len(bookmarks) == 0 {
			return "No bookmarks saved yet.", nil
		}
		return fmt.Sprintf("All bookmarks:\n%s", strings.Join(bookmarks, "\n")), nil

	case "delete":
		return "", fmt.Errorf("delete not yet implemented - bookmarks are stored in knowledge graph")

	default:
		return "", fmt.Errorf("unknown action: %s (use: save, search, list)", action)
	}
}
