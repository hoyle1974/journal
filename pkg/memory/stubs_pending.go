package memory

// This file contains stub type declarations for types that will be fully defined
// by subsequent tasks. The stubs allow this package to compile and tests to run
// before those tasks are complete.
//
// Task 3 will replace JournalAnalysis, Entity, OpenLoop, and NormalizeEntityStatus
// with the full implementation in analysis.go.
//
// Task 5 will replace QueryLog with the full implementation in query_nodes.go.
//
// When Task 3 or Task 5 are committed, delete the corresponding stubs from this file
// (or delete this file entirely once all stubs are replaced).

// Entity represents a person, project, or event mentioned in a journal entry.
// Full definition provided by Task 3 (analysis.go).
type Entity struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	SourceID string `json:"source_id"`
}

// OpenLoop represents a task or unanswered question from a journal entry.
// Full definition provided by Task 3 (analysis.go).
type OpenLoop struct {
	Task     string `json:"task"`
	Priority string `json:"priority"`
	SourceID string `json:"source_id"`
}

// JournalAnalysis is the structured output from analyzing a journal entry.
// Full definition provided by Task 3 (analysis.go).
type JournalAnalysis struct {
	Summary   string     `json:"summary"`
	Mood      string     `json:"mood"`
	Category  string     `json:"category"`
	Tags      []string   `json:"tags"`
	Entities  []Entity   `json:"entities"`
	OpenLoops []OpenLoop `json:"open_loops"`
	SourceID  string     `json:"source_id"`
}

// NormalizeEntityStatus normalizes entity status strings.
// Full implementation provided by Task 3 (analysis.go).
func NormalizeEntityStatus(status string) string {
	return status
}

// QueryLog represents a logged Q&A pair from the FOH loop.
// Full definition provided by Task 5 (query_nodes.go).
type QueryLog struct {
	UUID      string `firestore:"-" json:"uuid"`
	Question  string `firestore:"question" json:"question"`
	Answer    string `firestore:"answer" json:"answer"`
	Source    string `firestore:"source" json:"source"`
	Timestamp string `firestore:"timestamp" json:"timestamp"`
}
