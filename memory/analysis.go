package memory

import (
	"context"
	"fmt"
	"strings"

	memoryprompts "github.com/hoyle1974/memory/prompts"
)

const (
	EntityStatusPlanned    = "Planned"
	EntityStatusInProgress = "In-Progress"
	EntityStatusStalled    = "Stalled"
	EntityStatusCompleted  = "Completed"
)

const (
	dateDisplayLen  = 10
	maxTagLen       = 30
	maxTagWords     = 3
	maxSummaryRunes = 250 // cap summary to avoid storing runaway/repetitive LLM output
)

// Entity represents a person, project, or event mentioned in a journal entry.
type Entity struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	SourceID string `json:"source_id"`
}

// OpenLoop represents a task or unanswered question from a journal entry.
type OpenLoop struct {
	Task     string `json:"task"`
	Priority string `json:"priority"`
	SourceID string `json:"source_id"`
}

// JournalAnalysis is the structured output from analyzing a journal entry.
type JournalAnalysis struct {
	Summary   string     `json:"summary"`
	Mood      string     `json:"mood"`
	Category  string     `json:"category"` // work, personal, health, finance, logistics
	Tags      []string   `json:"tags"`
	Entities  []Entity   `json:"entities"`
	OpenLoops []OpenLoop `json:"open_loops"`
	SourceID  string     `json:"source_id"`
}

// parseKeyValueAnalysis parses key/value + list sections into structured fields.
func parseKeyValueAnalysis(text string) (summary, mood, category string, tags []string, entities []Entity, openLoops []OpenLoop, err error) {
	simple, sections := parseKeyValueMap(text)
	summary = simple["summary"]
	mood = simple["mood"]
	category = simple["category"]
	if tagStr := simple["tags"]; tagStr != "" {
		for _, s := range strings.Split(tagStr, ",") {
			if t := sanitizeTag(s); t != "" && len(tags) < 5 {
				tags = append(tags, t)
			}
		}
	}
	for _, line := range sections["entities"] {
		if strings.Contains(line, " | ") {
			parts := strings.SplitN(line, " | ", 3)
			if len(parts) >= 3 {
				entities = append(entities, Entity{
					Name:   strings.TrimSpace(parts[0]),
					Type:   strings.TrimSpace(parts[1]),
					Status: strings.TrimSpace(parts[2]),
				})
			}
		}
	}
	for _, line := range sections["open_loops"] {
		if strings.Contains(line, " | ") {
			parts := strings.SplitN(line, " | ", 2)
			if len(parts) >= 2 {
				openLoops = append(openLoops, OpenLoop{
					Task:     strings.TrimSpace(parts[0]),
					Priority: strings.TrimSpace(parts[1]),
				})
			}
		}
	}
	if summary == "" && mood == "" && category == "" && len(tags) == 0 && len(entities) == 0 && len(openLoops) == 0 {
		return "", "", "", nil, nil, nil, fmt.Errorf("no key/value data found")
	}
	return summary, mood, category, tags, entities, openLoops, nil
}

// AnalyzeJournalEntry uses the LLM to analyze a journal entry.
func (s *Store) AnalyzeJournalEntry(ctx context.Context, entryContent, entryUUID, entryTimestamp string) (*JournalAnalysis, error) {
	if len(entryContent) < 20 {
		return nil, nil
	}

	entryDate := entryTimestamp
	if len(entryDate) > dateDisplayLen {
		entryDate = truncateString(entryDate, dateDisplayLen)
	}
	if entryDate == "" {
		entryDate = "unknown"
	}

	prompt, err := memoryprompts.BuildJournalAnalyze(memoryprompts.JournalAnalyzeData{
		EntryID:   entryUUID,
		Date:      entryDate,
		EntryText: wrapAsUserData(sanitizePrompt(entryContent)),
	})
	if err != nil {
		return nil, fmt.Errorf("build journal analyze prompt: %w", err)
	}
	text, err := s.llm.Dispatch(ctx, LLMRequest{
		UserPrompt: prompt,
		MaxTokens:  512,
	})
	if err != nil {
		s.log.Warn("journal analysis failed", "error", err)
		return nil, fmt.Errorf("journal analysis: %w", err)
	}

	summary, mood, category, tags, entities, openLoops, parseErr := parseKeyValueAnalysis(strings.TrimSpace(text))
	if parseErr != nil {
		s.log.Warn("failed to parse journal analysis response", "error", parseErr)
		return nil, fmt.Errorf("journal analysis parse: %w", parseErr)
	}
	if len([]rune(summary)) > maxSummaryRunes {
		summary = string([]rune(summary)[:maxSummaryRunes]) + "…"
	}
	analysis := &JournalAnalysis{
		Summary:   summary,
		Mood:      mood,
		Category:  category,
		Tags:      tags,
		Entities:  entities,
		OpenLoops: openLoops,
	}
	for i := range analysis.Entities {
		if analysis.Entities[i].SourceID == "" {
			analysis.Entities[i].SourceID = entryUUID
		}
		analysis.Entities[i].Status = NormalizeEntityStatus(analysis.Entities[i].Status)
	}
	for j := range analysis.OpenLoops {
		if analysis.OpenLoops[j].SourceID == "" {
			analysis.OpenLoops[j].SourceID = entryUUID
		}
	}
	analysis.SourceID = entryUUID
	return analysis, nil
}

// sanitizeTag returns a tag suitable for storage, or empty string to drop.
func sanitizeTag(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	if len(t) > maxTagLen {
		return ""
	}
	if strings.Contains(t, ". ") {
		return ""
	}
	if strings.Contains(t, "?") || strings.Contains(t, " - ") {
		return ""
	}
	words := strings.Fields(t)
	if len(words) > maxTagWords {
		return ""
	}
	return t
}

// NormalizeEntityStatus maps LLM output to canonical status.
func NormalizeEntityStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "planned", "planning", "pending", "scheduled":
		return EntityStatusPlanned
	case "in-progress", "in progress", "ongoing", "active", "started":
		return EntityStatusInProgress
	case "stalled", "blocked", "on hold", "paused":
		return EntityStatusStalled
	case "completed", "done", "finished", "resolved":
		return EntityStatusCompleted
	default:
		if s == "" {
			return EntityStatusInProgress
		}
		return s
	}
}
