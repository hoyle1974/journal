package memory

// This file contains stub type declarations for types that will be fully defined
// by subsequent tasks. The stubs allow this package to compile and tests to run
// before those tasks are complete.
//
// Task 5 will replace QueryLog with the full implementation in query_nodes.go.
//
// When Task 5 is committed, delete this file entirely once all stubs are replaced.

// QueryLog represents a logged Q&A pair from the FOH loop.
// Full definition provided by Task 5 (query_nodes.go).
type QueryLog struct {
	UUID      string `firestore:"-" json:"uuid"`
	Question  string `firestore:"question" json:"question"`
	Answer    string `firestore:"answer" json:"answer"`
	Source    string `firestore:"source" json:"source"`
	Timestamp string `firestore:"timestamp" json:"timestamp"`
}
