package task

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/api/iterator"
)

const (
	TasksCollection = "tasks"
	StatusPending   = "pending"
	StatusActive    = "active"
	StatusCompleted = "completed"
	StatusAbandoned = "abandoned"
)

const reflectionSystemPrompt = `You are a summarizer. Given context about why a task was completed or abandoned, output exactly 1-2 short sentences of plain prose suitable for a journal reflection. Output plain text only—no JSON, no arrays, no code, no numbers or brackets. No preamble or quotes.`

// CreateTask creates a task in Firestore, generates an embedding for Content+SystemPrompt, and returns the task UUID.
func CreateTask(ctx context.Context, t *Task) (string, error) {
	ctx, span := infra.StartSpan(ctx, "task.create")
	defer span.End()

	if t == nil || t.Content == "" {
		return "", fmt.Errorf("task content is required")
	}

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return "", fmt.Errorf("no app in context")
	}
	projectID := app.Config().GoogleCloudProject

	textToEmbed := t.Content
	if t.SystemPrompt != "" {
		textToEmbed = t.Content + " " + t.SystemPrompt
	}
	embedding, err := infra.GenerateEmbedding(ctx, projectID, textToEmbed, infra.EmbedTaskRetrievalDocument)
	if err != nil {
		span.RecordError(err)
		return "", fmt.Errorf("generate embedding: %w", err)
	}

	uuid := infra.GenerateUUID()
	ts := time.Now().Format(time.RFC3339)
	if t.Timestamp != "" {
		ts = t.Timestamp
	}

	doc := map[string]interface{}{
		"parent_id":          t.ParentID,
		"content":            t.Content,
		"status":             normalizeStatus(t.Status),
		"due_date":           t.DueDate,
		"system_prompt":      t.SystemPrompt,
		"journal_entry_ids":  t.JournalEntryIDs,
		"memory_node_ids":    t.MemoryNodeIDs,
		"embedding":          firestore.Vector32(embedding),
		"timestamp":          ts,
	}
	if doc["journal_entry_ids"] == nil {
		doc["journal_entry_ids"] = []string{}
	}
	if doc["memory_node_ids"] == nil {
		doc["memory_node_ids"] = []string{}
	}

	_, err = client.Collection(TasksCollection).Doc(uuid).Set(ctx, doc)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	infra.LoggerFrom(ctx).Debug("task created", "uuid", uuid, "content", t.Content)
	span.SetAttributes(map[string]string{"uuid": uuid})
	return uuid, nil
}

// GetTask fetches a task by UUID.
func GetTask(ctx context.Context, uuid string) (*Task, error) {
	ctx, span := infra.StartSpan(ctx, "task.get")
	defer span.End()

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	doc, err := client.Collection(TasksCollection).Doc(uuid).Get(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	var t Task
	if err := doc.DataTo(&t); err != nil {
		span.RecordError(err)
		return nil, err
	}
	t.UUID = doc.Ref.ID
	return &t, nil
}

// UpdateTaskStatus updates the task's status. When status is completed or abandoned, it requires reflectionReason,
// calls Gemini to generate a 1-2 sentence summary, appends a journal entry with that summary, and appends the entry UUID to the task's journal_entry_ids.
func UpdateTaskStatus(ctx context.Context, uuid, newStatus, reflectionReason string) error {
	ctx, span := infra.StartSpan(ctx, "task.update_status")
	defer span.End()

	newStatus = normalizeStatus(newStatus)
	if newStatus != StatusCompleted && newStatus != StatusAbandoned {
		// No reflection required; just update status.
		return updateTaskStatusOnly(ctx, uuid, newStatus)
	}

	if reflectionReason == "" {
		return fmt.Errorf("reasoning is required when completing or abandoning a task")
	}

	existing, err := GetTask(ctx, uuid)
	if err != nil {
		span.RecordError(err)
		return err
	}

	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return fmt.Errorf("no app in context")
	}
	cfg := app.Config()

	userPrompt := fmt.Sprintf("Task: %s\n\nReason: %s",
		utils.WrapAsUserData(utils.SanitizePrompt(existing.Content)),
		utils.WrapAsUserData(utils.SanitizePrompt(reflectionReason)))

	summary, err := infra.GenerateContentSimple(ctx, reflectionSystemPrompt, userPrompt, cfg, &infra.GenConfig{
		MaxOutputTokens: 128,
	})
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("generate reflection summary: %w", err)
	}

	summary = utils.TruncateString(strings.TrimSpace(summary), 500)
	// Reject malformed output (e.g. model returned "[ 1 ]" or JSON); fall back to reason.
	if summary == "" || strings.HasPrefix(summary, "[") || strings.HasPrefix(summary, "{") {
		summary = reflectionReason
	}

	entryUUID, err := journal.AddEntry(ctx, summary, "system:task_engine", nil)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("add reflection entry: %w", err)
	}

	journalIDs := append([]string{}, existing.JournalEntryIDs...)
	journalIDs = append(journalIDs, entryUUID)

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return err
	}

	_, err = client.Collection(TasksCollection).Doc(uuid).Update(ctx, []firestore.Update{
		{Path: "status", Value: newStatus},
		{Path: "journal_entry_ids", Value: journalIDs},
	})
	if err != nil {
		span.RecordError(err)
		return err
	}

	infra.LoggerFrom(ctx).Info("task status updated with reflection", "uuid", uuid, "status", newStatus, "reflection_entry", entryUUID)
	span.SetAttributes(map[string]string{"uuid": uuid, "status": newStatus, "reflection_entry": entryUUID})
	return nil
}

// applyAddRemove appends addIDs (deduplicated) then removes removeIDs from the slice.
func applyAddRemove(current, addIDs, removeIDs []string) []string {
	seen := make(map[string]bool)
	for _, id := range current {
		if id != "" {
			seen[id] = true
		}
	}
	for _, id := range addIDs {
		if id != "" {
			seen[id] = true
		}
	}
	for _, id := range removeIDs {
		delete(seen, id)
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

func updateTaskStatusOnly(ctx context.Context, uuid, newStatus string) error {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(TasksCollection).Doc(uuid).Update(ctx, []firestore.Update{
		{Path: "status", Value: newStatus},
	})
	return err
}

// UpdateTaskOpts holds optional fields to update on a task. Only non-nil/non-empty fields are updated.
// When Content or SystemPrompt is set, the task embedding is recomputed.
// Add* and Remove* IDs are applied after other field updates: add then remove, deduplicated.
type UpdateTaskOpts struct {
	Content               *string
	ParentID              *string
	DueDate               *string
	SystemPrompt          *string
	AddJournalEntryIDs    []string // append these (deduplicated)
	RemoveJournalEntryIDs []string // remove these
	AddMemoryNodeIDs      []string
	RemoveMemoryNodeIDs   []string
}

// UpdateTask updates the given task with any non-nil opts. Recomputes embedding if Content or SystemPrompt changed.
func UpdateTask(ctx context.Context, uuid string, opts *UpdateTaskOpts) error {
	ctx, span := infra.StartSpan(ctx, "task.update")
	defer span.End()

	if opts == nil {
		return nil
	}

	existing, err := GetTask(ctx, uuid)
	if err != nil {
		span.RecordError(err)
		return err
	}

	var updates []firestore.Update
	content := existing.Content
	systemPrompt := existing.SystemPrompt
	if opts.Content != nil {
		content = *opts.Content
		updates = append(updates, firestore.Update{Path: "content", Value: content})
	}
	if opts.ParentID != nil {
		updates = append(updates, firestore.Update{Path: "parent_id", Value: *opts.ParentID})
	}
	if opts.DueDate != nil {
		updates = append(updates, firestore.Update{Path: "due_date", Value: *opts.DueDate})
	}
	if opts.SystemPrompt != nil {
		systemPrompt = *opts.SystemPrompt
		updates = append(updates, firestore.Update{Path: "system_prompt", Value: systemPrompt})
	}

	// Apply add/remove for journal and memory backlinks.
	journalIDs := existing.JournalEntryIDs
	if len(opts.AddJournalEntryIDs) > 0 || len(opts.RemoveJournalEntryIDs) > 0 {
		journalIDs = applyAddRemove(journalIDs, opts.AddJournalEntryIDs, opts.RemoveJournalEntryIDs)
		updates = append(updates, firestore.Update{Path: "journal_entry_ids", Value: journalIDs})
	}
	memoryIDs := existing.MemoryNodeIDs
	if len(opts.AddMemoryNodeIDs) > 0 || len(opts.RemoveMemoryNodeIDs) > 0 {
		memoryIDs = applyAddRemove(memoryIDs, opts.AddMemoryNodeIDs, opts.RemoveMemoryNodeIDs)
		updates = append(updates, firestore.Update{Path: "memory_node_ids", Value: memoryIDs})
	}

	if len(updates) == 0 {
		return nil
	}

	// Recompute embedding if content or system_prompt changed (for semantic search).
	if opts.Content != nil || opts.SystemPrompt != nil {
		app := infra.GetApp(ctx)
		if app == nil || app.Config() == nil {
			return fmt.Errorf("no app in context")
		}
		textToEmbed := content
		if systemPrompt != "" {
			textToEmbed = content + " " + systemPrompt
		}
		embedding, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, textToEmbed, infra.EmbedTaskRetrievalDocument)
		if err != nil {
			span.RecordError(err)
			return fmt.Errorf("generate embedding: %w", err)
		}
		updates = append(updates, firestore.Update{Path: "embedding", Value: firestore.Vector32(embedding)})
	}

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return err
	}
	_, err = client.Collection(TasksCollection).Doc(uuid).Update(ctx, updates)
	if err != nil {
		span.RecordError(err)
		return err
	}
	infra.LoggerFrom(ctx).Debug("task updated", "uuid", uuid)
	span.SetAttributes(map[string]string{"uuid": uuid})
	return nil
}

// GetOpenRootTasks returns root-level tasks (no parent) that are pending or active, newest first.
func GetOpenRootTasks(ctx context.Context, limit int) ([]Task, error) {
	ctx, span := infra.StartSpan(ctx, "task.get_open_roots")
	defer span.End()

	if limit <= 0 || limit > 50 {
		limit = 25
	}

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	iter := client.Collection(TasksCollection).
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
			return nil, err
		}
		var t Task
		if err := doc.DataTo(&t); err != nil {
			continue
		}
		t.UUID = doc.Ref.ID
		if t.ParentID != "" {
			continue
		}
		if t.Status != StatusPending && t.Status != StatusActive {
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

// QuerySimilarTasks performs a KNN vector search on the tasks collection.
func QuerySimilarTasks(ctx context.Context, queryVector []float32, limit int) ([]Task, error) {
	ctx, span := infra.StartSpan(ctx, "task.query_similar")
	defer span.End()

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	const distanceResultField = "_vector_distance"
	opts := &firestore.FindNearestOptions{DistanceResultField: distanceResultField}
	vectorQuery := client.Collection(TasksCollection).
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
			infra.LogVectorSearchFailed(ctx, TasksCollection, err, 0)
			span.RecordError(err)
			return nil, err
		}
		var t Task
		if err := doc.DataTo(&t); err != nil {
			infra.LoggerFrom(ctx).Debug("task document skip", "doc_id", doc.Ref.ID, "reason", err)
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

// NormalizeStatus returns a valid status value (pending, active, completed, abandoned).
func NormalizeStatus(s string) string {
	switch s {
	case StatusPending, StatusActive, StatusCompleted, StatusAbandoned:
		return s
	case "":
		return StatusPending
	}
	return StatusPending
}

func normalizeStatus(s string) string {
	return NormalizeStatus(s)
}
