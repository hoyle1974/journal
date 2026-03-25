package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// TaskCreateIdempotencyWindow is the window in which we consider a task "recent" for deduplication.
// Prevents duplicate tasks when both process-entry (agency) and the LLM create_task run concurrently for the same entry.
const TaskCreateIdempotencyWindow = 30 * time.Second

// TaskSemanticDedupThreshold is the cosine distance below which a new task is considered a semantic duplicate
// of an existing open (pending/active) task. Cosine distance = 1 - cosine_similarity; 0.15 ≈ 85% similarity.
const TaskSemanticDedupThreshold = 0.15

const reflectionSystemPrompt = `You are a summarizer. Given context about why a task was completed or abandoned, output exactly 1-2 short sentences of plain prose suitable for a journal reflection. Output plain text only—no JSON, no arrays, no code, no numbers or brackets. No preamble or quotes.`

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

// NormalizeTaskStatus returns a valid status value (pending, active, completed, abandoned).
func NormalizeTaskStatus(s string) string {
	switch s {
	case TaskStatusPending, TaskStatusActive, TaskStatusCompleted, TaskStatusAbandoned:
		return s
	}
	return TaskStatusPending
}

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
// created within the given window, or empty string if none found.
func findRecentDuplicateTask(ctx context.Context, client *firestore.Client, entryUUID, normalizedContent string, within time.Duration) (string, error) {
	if entryUUID == "" || normalizedContent == "" {
		return "", nil
	}
	cutoff := time.Now().Add(-within)
	iter := client.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeTask).
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
			return "", fmt.Errorf("iterate tasks: %w", err)
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

// findSimilarOpenTask returns the UUID of an existing pending or active task whose embedding is within
// TaskSemanticDedupThreshold cosine distance of the provided vector, or empty string if none found.
func findSimilarOpenTask(ctx context.Context, client *firestore.Client, embedding []float32) (string, error) {
	threshold := TaskSemanticDedupThreshold
	opts := &firestore.FindNearestOptions{DistanceThreshold: &threshold}
	vectorQuery := client.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeTask).
		FindNearest("embedding", firestore.Vector32(embedding), 5, firestore.DistanceMeasureCosine, opts)
	iter := vectorQuery.Documents(ctx)
	defer iter.Stop()
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return "", fmt.Errorf("find similar open tasks: %w", err)
		}
		data := doc.Data()
		status, _ := data["status"].(string)
		if status != TaskStatusPending && status != TaskStatusActive {
			continue
		}
		return doc.Ref.ID, nil
	}
	return "", nil
}

// CreateTask creates a task in the journal collection (node_type=task), generates an embedding for
// Content+SystemPrompt, and returns the task UUID.
func (s *Store) CreateTask(ctx context.Context, t *Task) (string, error) {
	if t == nil || t.Content == "" {
		return "", fmt.Errorf("task content is required")
	}

	// Idempotency: if this task is linked to exactly one entry, avoid creating a duplicate when agency and LLM both create for the same content.
	if len(t.JournalEntryIDs) == 1 {
		entryUUID := t.JournalEntryIDs[0]
		existingUUID, err := findRecentDuplicateTask(ctx, s.db, entryUUID, normalizeContentForDedup(t.Content), TaskCreateIdempotencyWindow)
		if err != nil {
			s.log.Debug("task create idempotency check failed", "entry_uuid", entryUUID, "error", err)
			// Proceed with create on check failure so we don't block task creation
		} else if existingUUID != "" {
			s.log.Info("task create idempotent: returning existing", "entry_uuid", entryUUID, "existing_uuid", existingUUID, "content", t.Content)
			return existingUUID, nil
		}
	}

	textToEmbed := t.Content
	if t.SystemPrompt != "" {
		textToEmbed = t.Content + " " + t.SystemPrompt
	}
	embedding, err := s.embedder.GenerateEmbedding(ctx, textToEmbed, EmbedTaskRetrievalDocument)
	if err != nil {
		return "", fmt.Errorf("generate embedding: %w", err)
	}

	// Semantic dedup: skip creation if a semantically equivalent open task already exists.
	// This catches wording variations that slip past the exact-text idempotency check above.
	if similarUUID, err := findSimilarOpenTask(ctx, s.db, embedding); err != nil {
		s.log.Debug("task semantic dedup check failed", "error", err)
		// Non-fatal: proceed with create so we never silently drop a genuine new task.
	} else if similarUUID != "" {
		s.log.Info("task create skipped: semantically similar open task exists", "existing_uuid", similarUUID, "content", t.Content)
		return similarUUID, nil
	}

	uuid := generateUUID()
	ts := time.Now().Format(time.RFC3339)
	if t.Timestamp != "" {
		ts = t.Timestamp
	}

	doc := map[string]interface{}{
		"node_type":           NodeTypeTask,
		"significance_weight": 0.7,
		"parent_id":           t.ParentID,
		"content":             t.Content,
		"status":              NormalizeTaskStatus(t.Status),
		"due_date":            t.DueDate,
		"system_prompt":       t.SystemPrompt,
		"journal_entry_ids":   t.JournalEntryIDs,
		"memory_node_ids":     t.MemoryNodeIDs,
		"embedding":           firestore.Vector32(embedding),
		"timestamp":           ts,
	}
	if doc["journal_entry_ids"] == nil {
		doc["journal_entry_ids"] = []string{}
	}
	if doc["memory_node_ids"] == nil {
		doc["memory_node_ids"] = []string{}
	}

	_, err = s.db.Collection(KnowledgeCollection).Doc(uuid).Set(ctx, doc)
	if err != nil {
		return "", fmt.Errorf("set task: %w", err)
	}

	s.log.Debug("task created", "uuid", uuid, "content", t.Content)
	return uuid, nil
}

// GetTask fetches a task by UUID.
func (s *Store) GetTask(ctx context.Context, uuid string) (*Task, error) {
	doc, err := s.db.Collection(KnowledgeCollection).Doc(uuid).Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}

	var t Task
	if err := doc.DataTo(&t); err != nil {
		return nil, fmt.Errorf("decode task: %w", err)
	}
	t.UUID = doc.Ref.ID
	return &t, nil
}

// UpdateTaskStatus updates the task's status. When status is completed or abandoned, it requires reflectionReason,
// calls the LLM to generate a 1-2 sentence summary, appends a journal entry with that summary, and appends the entry UUID to the task's journal_entry_ids.
func (s *Store) UpdateTaskStatus(ctx context.Context, uuid, newStatus, reflectionReason string) error {
	newStatus = NormalizeTaskStatus(newStatus)
	if newStatus != TaskStatusCompleted && newStatus != TaskStatusAbandoned {
		// No reflection required; just update status.
		return s.updateTaskStatusOnly(ctx, uuid, newStatus)
	}

	if reflectionReason == "" {
		return fmt.Errorf("reasoning is required when completing or abandoning a task")
	}

	existing, err := s.GetTask(ctx, uuid)
	if err != nil {
		return fmt.Errorf("get task for status update: %w", err)
	}

	userPrompt := fmt.Sprintf("Task: %s\n\nReason: %s",
		wrapAsUserData(sanitizePrompt(existing.Content)),
		wrapAsUserData(sanitizePrompt(reflectionReason)))

	summary, err := s.llm.Dispatch(ctx, LLMRequest{
		SystemPrompt: reflectionSystemPrompt,
		UserPrompt:   userPrompt,
		MaxTokens:    128,
	})
	if err != nil {
		return fmt.Errorf("generate reflection summary: %w", err)
	}

	summary = truncateString(summary, 500)
	// Reject malformed output (e.g. model returned "[ 1 ]" or JSON); fall back to reason.
	if summary == "" || strings.HasPrefix(summary, "[") || strings.HasPrefix(summary, "{") {
		summary = reflectionReason
	}

	entryUUID, err := s.AddEntry(ctx, summary, "system:task_engine", nil, "")
	if err != nil {
		return fmt.Errorf("add reflection entry: %w", err)
	}

	journalIDs := append([]string{}, existing.JournalEntryIDs...)
	journalIDs = append(journalIDs, entryUUID)

	_, err = s.db.Collection(KnowledgeCollection).Doc(uuid).Update(ctx, []firestore.Update{
		{Path: "status", Value: newStatus},
		{Path: "journal_entry_ids", Value: journalIDs},
	})
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}

	s.log.Info("task status updated with reflection", "uuid", uuid, "status", newStatus, "reflection_entry", entryUUID)
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

func (s *Store) updateTaskStatusOnly(ctx context.Context, uuid, newStatus string) error {
	_, err := s.db.Collection(KnowledgeCollection).Doc(uuid).Update(ctx, []firestore.Update{
		{Path: "status", Value: newStatus},
	})
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	return nil
}

// UpdateTask updates the given task with any non-nil opts. Recomputes embedding if Content or SystemPrompt changed.
func (s *Store) UpdateTask(ctx context.Context, uuid string, opts *UpdateTaskOpts) error {
	if opts == nil {
		return nil
	}

	existing, err := s.GetTask(ctx, uuid)
	if err != nil {
		return fmt.Errorf("get task for update: %w", err)
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
		textToEmbed := content
		if systemPrompt != "" {
			textToEmbed = content + " " + systemPrompt
		}
		embedding, err := s.embedder.GenerateEmbedding(ctx, textToEmbed, EmbedTaskRetrievalDocument)
		if err != nil {
			return fmt.Errorf("generate embedding: %w", err)
		}
		updates = append(updates, firestore.Update{Path: "embedding", Value: firestore.Vector32(embedding)})
	}

	_, err = s.db.Collection(KnowledgeCollection).Doc(uuid).Update(ctx, updates)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	s.log.Debug("task updated", "uuid", uuid)
	return nil
}
