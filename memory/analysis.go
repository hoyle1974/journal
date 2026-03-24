package memory

import "strings"

const (
	EntityStatusPlanned    = "Planned"
	EntityStatusInProgress = "In-Progress"
	EntityStatusStalled    = "Stalled"
	EntityStatusCompleted  = "Completed"
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
