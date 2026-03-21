package memory

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// GetOpenRootTasks returns root-level tasks (no parent) that are pending or active, newest first.
func (s *Store) GetOpenRootTasks(ctx context.Context, limit int) ([]Task, error) {
	if limit <= 0 || limit > 50 {
		limit = 25
	}

	iter := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeTask).
		OrderBy("timestamp", firestore.Desc).
		Limit(100).
		Documents(ctx)
	defer iter.Stop()

	var tasks []Task
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("iterate tasks: %w", err)
		}
		var t Task
		if err := doc.DataTo(&t); err != nil {
			s.log.Warn("GetOpenRootTasks: skipping task document", "doc_id", doc.Ref.ID, "error", err)
			continue
		}
		t.UUID = doc.Ref.ID
		if t.ParentID != "" {
			continue
		}
		if t.Status != TaskStatusPending && t.Status != TaskStatusActive {
			continue
		}
		tasks = append(tasks, t)
		if len(tasks) >= limit {
			break
		}
	}

	return tasks, nil
}

// QuerySimilarTasks performs a KNN vector search on the journal collection filtered to node_type=task.
func (s *Store) QuerySimilarTasks(ctx context.Context, queryVector []float32, limit int) ([]Task, error) {
	const distanceResultField = "_vector_distance"
	opts := &firestore.FindNearestOptions{DistanceResultField: distanceResultField}
	vectorQuery := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeTask).
		FindNearest("embedding", firestore.Vector32(queryVector), limit, firestore.DistanceMeasureCosine, opts)
	iter := vectorQuery.Documents(ctx)
	defer iter.Stop()

	var tasks []Task
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			s.logVectorSearchFailed(KnowledgeCollection, err, 0)
			return nil, fmt.Errorf("iterate tasks: %w", err)
		}
		var t Task
		if err := doc.DataTo(&t); err != nil {
			s.log.Warn("QuerySimilarTasks: skipping task document", "doc_id", doc.Ref.ID, "error", err)
			continue
		}
		t.UUID = doc.Ref.ID
		tasks = append(tasks, t)
	}

	return tasks, nil
}

// FormatTasksForContext formats tasks for LLM context (uuid, status, due_date, content).
// Use due=(not set) when DueDate is empty so the agent sees the field is present.
func FormatTasksForContext(tasks []Task, maxChars int) string {
	if len(tasks) == 0 {
		return "No tasks found."
	}
	var out []string
	n := 0
	for _, t := range tasks {
		due := t.DueDate
		if due == "" {
			due = "(not set)"
		}
		line := fmt.Sprintf("[%s] status=%s due=%s | %s", t.UUID, t.Status, due, truncateString(t.Content, 120))
		if n+len(line) > maxChars {
			break
		}
		out = append(out, line)
		n += len(line)
	}
	return strings.Join(out, "\n")
}
