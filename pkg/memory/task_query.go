package memory

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/api/iterator"
)

// GetOpenRootTasks returns root-level tasks (no parent) that are pending or active, newest first.
// env supplies Firestore; pass from the caller (e.g. ToolEnv).
func GetOpenRootTasks(ctx context.Context, env infra.ToolEnv, limit int) ([]Task, error) {
	ctx, span := infra.StartSpan(ctx, "tasks.get_open_roots")
	defer span.End()

	if limit <= 0 || limit > 50 {
		limit = 25
	}

	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("firestore client: %w", err)
	}

	iter := client.Collection(KnowledgeCollection).
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
			span.RecordError(err)
			return nil, fmt.Errorf("iterate tasks: %w", err)
		}
		var t Task
		if err := doc.DataTo(&t); err != nil {
			infra.LoggerFrom(ctx).Warn("GetOpenRootTasks: skipping task document", "doc_id", doc.Ref.ID, "error", err)
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

	span.SetAttributes(map[string]string{"results_count": fmt.Sprintf("%d", len(tasks))})
	return tasks, nil
}

// QuerySimilarTasks performs a KNN vector search on the journal collection filtered to node_type=task.
// env supplies Firestore; pass from the caller (e.g. ToolEnv).
func QuerySimilarTasks(ctx context.Context, env infra.ToolEnv, queryVector []float32, limit int) ([]Task, error) {
	ctx, span := infra.StartSpan(ctx, "tasks.query_similar")
	defer span.End()

	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("firestore client: %w", err)
	}

	const distanceResultField = "_vector_distance"
	opts := &firestore.FindNearestOptions{DistanceResultField: distanceResultField}
	vectorQuery := client.Collection(KnowledgeCollection).
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
			infra.LogVectorSearchFailed(ctx, KnowledgeCollection, err, 0)
			span.RecordError(err)
			return nil, fmt.Errorf("iterate tasks: %w", err)
		}
		var t Task
		if err := doc.DataTo(&t); err != nil {
			infra.LoggerFrom(ctx).Warn("QuerySimilarTasks: skipping task document", "doc_id", doc.Ref.ID, "error", err)
			continue
		}
		t.UUID = doc.Ref.ID
		tasks = append(tasks, t)
	}

	span.SetAttributes(map[string]string{"results_count": fmt.Sprintf("%d", len(tasks))})
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
		line := fmt.Sprintf("[%s] status=%s due=%s | %s", t.UUID, t.Status, due, utils.TruncateString(t.Content, 120))
		if n+len(line) > maxChars {
			break
		}
		out = append(out, line)
		n += len(line)
	}
	return strings.Join(out, "\n")
}
