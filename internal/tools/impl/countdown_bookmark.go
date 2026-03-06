package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/memory"
)

// HandleCountdown manages countdown events (uses knowledge/embedding).
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
		id, err := memory.UpsertKnowledge(ctx, fmt.Sprintf("Countdown: %s", name), "countdown", metadata, nil)
		if err != nil {
			return "", err
		}
		daysUntil := int(time.Until(targetDate).Hours() / 24)
		return fmt.Sprintf("Countdown '%s' created for %s (%d days away). ID: %s", name, dateStr, daysUntil, id), nil

	case "check":
		if name == "" {
			return "", fmt.Errorf("name required for check")
		}
		queryVec, err := embeddingForContext(ctx, fmt.Sprintf("Countdown: %s", name))
		if err != nil {
			return "", err
		}
		nodes, err := memory.QuerySimilarNodes(ctx, queryVec, 5)
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
		queryVec, err := embeddingForContext(ctx, "Countdown event")
		if err != nil {
			return "", err
		}
		nodes, err := memory.QuerySimilarNodes(ctx, queryVec, 20)
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

// HandleBookmark manages bookmarks.
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
		id, err := memory.UpsertKnowledge(ctx, fmt.Sprintf("Bookmark: %s", title), "bookmark", string(metaJSON), nil)
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
		queryVec, err := embeddingForContext(ctx, fmt.Sprintf("Bookmark %s", searchQuery))
		if err != nil {
			return "", err
		}
		nodes, err := memory.QuerySimilarNodes(ctx, queryVec, 10)
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
		queryVec, err := embeddingForContext(ctx, "Bookmark")
		if err != nil {
			return "", err
		}
		nodes, err := memory.QuerySimilarNodes(ctx, queryVec, 20)
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

func embeddingForContext(ctx context.Context, text string) ([]float32, error) {
	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("no app in context for embedding")
	}
	return infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, text)
}
