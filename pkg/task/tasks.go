package task

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/infra"
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

	// TaskCreateIdempotencyWindow is the window in which we consider a task "recent" for deduplication.
	// Prevents duplicate tasks when both process-entry (agency) and the LLM create_task run concurrently for the same entry.
	TaskCreateIdempotencyWindow = 30 * time.Second
)

const reflectionSystemPrompt = `You are a summarizer. Given context about why a task was completed or abandoned, output exactly 1-2 short sentences of plain prose suitable for a journal reflection. Output plain text only—no JSON, no arrays, no code, no numbers or brackets. No preamble or quotes.`

// normalizeContentForDedup normalizes task content for idempotency comparison (trim, lowercase, collapse whitespace).
func normalizeContentForDedup(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	content = strings.ToLower(content)
	parts := strings.Fields(content)
	return strings.Join(parts, " ")
}

// findRecentDuplicateTask returns the UUID of a task linked to entryUUID with the same normalized content
// created within the given window, or empty string if none found. Used to avoid duplicate tasks when
// process-entry (agency) and the LLM create_task run concurrently.
func findRecentDuplicateTask(ctx context.Context, client *firestore.Client, entryUUID, normalizedContent string, within time.Duration) (string, error) {
	if entryUUID == "" || normalizedContent == "" {
		return "", nil
	}
	cutoff := time.Now().Add(-within)
	iter := client.Collection(TasksCollection).
		Where("journal_entry_ids", "array-contains", entryUUID).
		OrderBy("timestamp", firestore.Desc).
		Limit(20).
		Documents(ctx)
	defer iter.Stop()
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return "", err
		}
		var task Task
		if err := doc.DataTo(&task); err != nil {
			continue
		}
		task.UUID = doc.Ref.ID
		parsed, err := time.Parse(time.RFC3339, task.Timestamp)
		if err != nil || time.Since(parsed) > within {
			continue
		}
		if parsed.Before(cutoff) {
			continue
		}
		if normalizeContentForDedup(task.Content) == normalizedContent {
			return task.UUID, nil
		}
	}
	return "", nil
}

// CreateTask creates a task in Firestore, generates an embedding for Content+SystemPrompt, and returns the task UUID.
// env supplies Firestore and Config; pass from the caller (e.g. ToolEnv).
func CreateTask(ctx context.Context, env infra.ToolEnv, t *Task) (string, error) {
	ctx, span := infra.StartSpan(ctx, "task.create")
	defer span.End()

	if t == nil || t.Content == "" {
		return "", fmt.Errorf("task content is required")
	}

	if env == nil || env.Config() == nil {
		return "", fmt.Errorf("env and config required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	// Idempotency: if this task is linked to exactly one entry, avoid creating a duplicate when agency and LLM both create for the same content.
	if len(t.JournalEntryIDs) == 1 {
		entryUUID := t.JournalEntryIDs[0]
		existingUUID, err := findRecentDuplicateTask(ctx, client, entryUUID, normalizeContentForDedup(t.Content), TaskCreateIdempotencyWindow)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("task create idempotency check failed", "entry_uuid", entryUUID, "error", err)
			// Proceed with create on check failure so we don't block task creation
		} else if existingUUID != "" {
			infra.LoggerFrom(ctx).Info("task create idempotent: returning existing", "entry_uuid", entryUUID, "existing_uuid", existingUUID, "content", t.Content)
			span.SetAttributes(map[string]string{"uuid": existingUUID, "idempotent": "true"})
			return existingUUID, nil
		}
	}

	projectID := env.Config().GoogleCloudProject

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

// GetTask fetches a task by UUID. env supplies Firestore; pass from the caller (e.g. ToolEnv).
func GetTask(ctx context.Context, env infra.ToolEnv, uuid string) (*Task, error) {
	ctx, span := infra.StartSpan(ctx, "task.get")
	defer span.End()

	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
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
// env supplies Firestore, Config, and Dispatch; pass from the caller (e.g. ToolEnv).
func UpdateTaskStatus(ctx context.Context, env infra.ToolEnv, uuid, newStatus, reflectionReason string) error {
	ctx, span := infra.StartSpan(ctx, "task.update_status")
	defer span.End()

	newStatus = normalizeStatus(newStatus)
	if newStatus != StatusCompleted && newStatus != StatusAbandoned {
		// No reflection required; just update status.
		return updateTaskStatusOnly(ctx, env, uuid, newStatus)
	}

	if reflectionReason == "" {
		return fmt.Errorf("reasoning is required when completing or abandoning a task")
	}

	existing, err := GetTask(ctx, env, uuid)
	if err != nil {
		span.RecordError(err)
		return err
	}

	if env == nil || env.Config() == nil {
		return fmt.Errorf("env and config required")
	}
	cfg := env.Config()
	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return err
	}

	userPrompt := fmt.Sprintf("Task: %s\n\nReason: %s",
		utils.WrapAsUserData(utils.SanitizePrompt(existing.Content)),
		utils.WrapAsUserData(utils.SanitizePrompt(reflectionReason)))

	summary, err := infra.GenerateContentSimple(ctx, env, reflectionSystemPrompt, userPrompt, cfg, &infra.GenConfig{
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

	entryUUID, err := journal.AddEntry(ctx, client, summary, "system:task_engine", nil)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("add reflection entry: %w", err)
	}

	journalIDs := append([]string{}, existing.JournalEntryIDs...)
	journalIDs = append(journalIDs, entryUUID)

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

func updateTaskStatusOnly(ctx context.Context, env infra.ToolEnv, uuid, newStatus string) error {
	if env == nil {
		return fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
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
// env supplies Firestore and Config; pass from the caller (e.g. ToolEnv).
func UpdateTask(ctx context.Context, env infra.ToolEnv, uuid string, opts *UpdateTaskOpts) error {
	ctx, span := infra.StartSpan(ctx, "task.update")
	defer span.End()

	if opts == nil {
		return nil
	}

	if env == nil {
		return fmt.Errorf("env required")
	}
	existing, err := GetTask(ctx, env, uuid)
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
		cfg := env.Config()
		if cfg == nil {
			return fmt.Errorf("env config required")
		}
		textToEmbed := content
		if systemPrompt != "" {
			textToEmbed = content + " " + systemPrompt
		}
		embedding, err := infra.GenerateEmbedding(ctx, cfg.GoogleCloudProject, textToEmbed, infra.EmbedTaskRetrievalDocument)
		if err != nil {
			span.RecordError(err)
			return fmt.Errorf("generate embedding: %w", err)
		}
		updates = append(updates, firestore.Update{Path: "embedding", Value: firestore.Vector32(embedding)})
	}

	client, err := env.Firestore(ctx)
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
// env supplies Firestore; pass from the caller (e.g. ToolEnv).
func GetOpenRootTasks(ctx context.Context, env infra.ToolEnv, limit int) ([]Task, error) {
	ctx, span := infra.StartSpan(ctx, "task.get_open_roots")
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
// env supplies Firestore; pass from the caller (e.g. ToolEnv).
func QuerySimilarTasks(ctx context.Context, env infra.ToolEnv, queryVector []float32, limit int) ([]Task, error) {
	ctx, span := infra.StartSpan(ctx, "task.query_similar")
	defer span.End()

	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
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
