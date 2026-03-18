package task

import "cloud.google.com/go/firestore"

// Task represents a todo/task with optional hierarchy and backlinks to journal and memory.
// A "project" is simply a Task with subtasks (child tasks whose ParentID == this task's UUID).
type Task struct {
	UUID            string             `firestore:"-" json:"uuid"`
	ParentID        string             `firestore:"parent_id" json:"parent_id"`
	Content         string             `firestore:"content" json:"content"`
	Status          string             `firestore:"status" json:"status"` // pending, active, completed, abandoned
	DueDate         string             `firestore:"due_date" json:"due_date"`
	SystemPrompt    string             `firestore:"system_prompt" json:"system_prompt"`
	Dependencies    []string           `firestore:"dependencies" json:"dependencies"`
	IsSequential    bool               `firestore:"is_sequential" json:"is_sequential"`
	JournalEntryIDs []string           `firestore:"journal_entry_ids" json:"journal_entry_ids"`
	MemoryNodeIDs   []string           `firestore:"memory_node_ids" json:"memory_node_ids"`
	Embedding       firestore.Vector32 `firestore:"embedding" json:"-"`
	Timestamp       string             `firestore:"timestamp" json:"timestamp"`
}
