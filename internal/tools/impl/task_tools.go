package impl

import (
	"context"
	"strings"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/task"
	"github.com/jackstrohm/jot/tools"
)

// parseCommaSeparatedIDs splits s by comma and returns non-empty trimmed UUIDs.
func parseCommaSeparatedIDs(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		id := strings.TrimSpace(part)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func init() {
	registerTaskTools()
}

func registerTaskTools() {
	tools.Register(&tools.Tool{
		Name:        "create_task",
		Description: "Create a new task. Optionally set parent_id (for sublists), due_date (YYYY-MM-DD), and system_prompt (instructions for the LLM). Links the task to the current journal entry when available.",
		Category:    "task",
		Params: []tools.Param{
			tools.RequiredStringParam("content", "Task description or title"),
			tools.OptionalStringParam("parent_id", "Parent task UUID for hierarchy"),
			tools.OptionalStringParam("due_date", "Due date (YYYY-MM-DD)"),
			tools.OptionalStringParam("system_prompt", "Instructions for the LLM when working on this task"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			content, ok := args.RequiredString("content")
			if !ok {
				return tools.MissingParam("content")
			}
			parentID := args.String("parent_id", "")
			dueDate := args.String("due_date", "")
			systemPrompt := args.String("system_prompt", "")

			t := &task.Task{
				Content:      content,
				ParentID:     parentID,
				DueDate:      dueDate,
				SystemPrompt: systemPrompt,
				Status:       task.StatusPending,
			}
			if cur := agent.CurrentEntryUUIDFrom(ctx); cur != "" {
				t.JournalEntryIDs = []string{cur}
			}

			uuid, err := task.CreateTask(ctx, t)
			if err != nil {
				return tools.Fail("Error creating task: %v", err)
			}
			if dueDate != "" {
				return tools.OK("Task created (ID: %s, due: %s)", uuid, dueDate)
			}
			return tools.OK("Task created (ID: %s)", uuid)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_task",
		Description: "Get full details of a single task by ID (content, status, due_date, system_prompt, journal_entry_ids, memory_node_ids). Use when the user asks for due dates or details of a specific task, or before updating backlinks.",
		Category:    "task",
		Params: []tools.Param{
			tools.RequiredStringParam("task_id", "Task UUID"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			taskID, ok := args.RequiredString("task_id")
			if !ok {
				return tools.MissingParam("task_id")
			}
			t, err := task.GetTask(ctx, taskID)
			if err != nil {
				return tools.Fail("Error fetching task: %v", err)
			}
			due := t.DueDate
			if due == "" {
				due = "(not set)"
			}
			journalIDs := strings.Join(t.JournalEntryIDs, ",")
			if journalIDs == "" {
				journalIDs = "(none)"
			}
			memoryIDs := strings.Join(t.MemoryNodeIDs, ",")
			if memoryIDs == "" {
				memoryIDs = "(none)"
			}
			return tools.OK("Task %s: status=%s due=%s content=%s system_prompt=%s journal_entry_ids=%s memory_node_ids=%s", t.UUID, t.Status, due, t.Content, t.SystemPrompt, journalIDs, memoryIDs)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "update_task",
		Description: "Update a task's editable fields. Provide task_id and any of: content, parent_id, due_date (YYYY-MM-DD or empty to clear), system_prompt; or add/remove journal or memory backlinks (comma-separated UUIDs). Only provided fields are changed. Use update_task_status to change status.",
		Category:    "task",
		Params: []tools.Param{
			tools.RequiredStringParam("task_id", "Task UUID"),
			tools.OptionalStringParam("content", "New task description/title"),
			tools.OptionalStringParam("parent_id", "New parent task UUID, or empty to make root"),
			tools.OptionalStringParam("due_date", "Due date (YYYY-MM-DD), or empty to clear"),
			tools.OptionalStringParam("system_prompt", "Instructions for the LLM when working on this task"),
			tools.OptionalStringParam("add_journal_entry_ids", "Comma-separated journal entry UUIDs to link to this task"),
			tools.OptionalStringParam("remove_journal_entry_ids", "Comma-separated journal entry UUIDs to unlink from this task"),
			tools.OptionalStringParam("add_memory_node_ids", "Comma-separated knowledge node UUIDs to link to this task"),
			tools.OptionalStringParam("remove_memory_node_ids", "Comma-separated knowledge node UUIDs to unlink from this task"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			taskID, ok := args.RequiredString("task_id")
			if !ok {
				return tools.MissingParam("task_id")
			}
			opts := &task.UpdateTaskOpts{
				Content:      args.OptionalString("content"),
				ParentID:     args.OptionalString("parent_id"),
				DueDate:      args.OptionalString("due_date"),
				SystemPrompt: args.OptionalString("system_prompt"),
			}
			if s := args.OptionalString("add_journal_entry_ids"); s != nil && *s != "" {
				opts.AddJournalEntryIDs = parseCommaSeparatedIDs(*s)
			}
			if s := args.OptionalString("remove_journal_entry_ids"); s != nil && *s != "" {
				opts.RemoveJournalEntryIDs = parseCommaSeparatedIDs(*s)
			}
			if s := args.OptionalString("add_memory_node_ids"); s != nil && *s != "" {
				opts.AddMemoryNodeIDs = parseCommaSeparatedIDs(*s)
			}
			if s := args.OptionalString("remove_memory_node_ids"); s != nil && *s != "" {
				opts.RemoveMemoryNodeIDs = parseCommaSeparatedIDs(*s)
			}
			hasEdit := opts.Content != nil || opts.ParentID != nil || opts.DueDate != nil || opts.SystemPrompt != nil ||
				len(opts.AddJournalEntryIDs) > 0 || len(opts.RemoveJournalEntryIDs) > 0 ||
				len(opts.AddMemoryNodeIDs) > 0 || len(opts.RemoveMemoryNodeIDs) > 0
			if !hasEdit {
				return tools.Fail("provide at least one field to update: content, parent_id, due_date, system_prompt, or add/remove journal/memory IDs")
			}
			err := task.UpdateTask(ctx, taskID, opts)
			if err != nil {
				return tools.Fail("Error updating task: %v", err)
			}
			return tools.OK("Task %s updated", taskID)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "update_task_status",
		Description: "Update a task's status. Use status: pending, active, completed, or abandoned. When marking completed or abandoned, reasoning is required and a reflection is saved to the journal.",
		Category:    "task",
		Params: []tools.Param{
			tools.RequiredStringParam("task_id", "Task UUID"),
			tools.EnumParam("status", "New status", true, []string{"pending", "active", "completed", "abandoned"}),
			tools.OptionalStringParam("reasoning", "Reason for the status change (required when completing or abandoning)"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			taskID, ok := args.RequiredString("task_id")
			if !ok {
				return tools.MissingParam("task_id")
			}
			status, ok := args.RequiredString("status")
			if !ok {
				return tools.MissingParam("status")
			}
			reasoning := args.String("reasoning", "")

			if (status == task.StatusCompleted || status == task.StatusAbandoned) && reasoning == "" {
				return tools.Fail("reasoning is required when marking a task as completed or abandoned")
			}

			err := task.UpdateTaskStatus(ctx, taskID, status, reasoning)
			if err != nil {
				return tools.Fail("Error updating task: %v", err)
			}
			return tools.OK("Task %s updated to %s", taskID, status)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "search_tasks",
		Description: "Search tasks by semantic similarity to the query, or list open root-level tasks. If query is empty or omitted, returns your open todo list roots (same as in context). Optionally filter by status (pending, active, completed, abandoned). Returns one line per task: uuid, status, due date (or 'not set'), and content. Use get_task for full details of a single task.",
		Category:    "task",
		Params: []tools.Param{
			tools.OptionalStringParam("query", "Natural language search query; omit or leave empty to list open root-level tasks"),
			tools.LimitParam(10, 20),
			tools.OptionalStringParam("status", "Filter by status (pending, active, completed, abandoned)"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			query := args.String("query", "")
			query = strings.TrimSpace(query)
			limit := args.IntBounded("limit", 10, 1, 20)
			statusFilter := args.String("status", "")

			var tasks []task.Task
			var err error
			if query == "" {
				// List open root-level tasks (same as OPEN TODO LIST ROOTS in prompt).
				tasks, err = task.GetOpenRootTasks(ctx, limit*2)
				if err != nil {
					return tools.Fail("Error listing tasks: %v", err)
				}
			} else {
				app := infra.GetApp(ctx)
				if app == nil || app.Config() == nil {
					return tools.Fail("Error: no app in context")
				}
				vec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, query, infra.EmbedTaskRetrievalDocument)
				if err != nil {
					return tools.Fail("Error generating embedding: %v", err)
				}
				tasks, err = task.QuerySimilarTasks(ctx, vec, limit*2)
				if err != nil {
					return tools.Fail("Error searching tasks: %v", err)
				}
			}

			if statusFilter != "" {
				norm := task.NormalizeStatus(statusFilter) // pending, active, completed, abandoned
				filtered := tasks[:0]
				for _, t := range tasks {
					if t.Status == norm {
						filtered = append(filtered, t)
					}
				}
				tasks = filtered
				if len(tasks) > limit {
					tasks = tasks[:limit]
				}
			} else if len(tasks) > limit {
				tasks = tasks[:limit]
			}

			if len(tasks) == 0 {
				return tools.OK("No tasks found.")
			}
			return tools.OK("Found %d task(s):\n%s", len(tasks), task.FormatTasksForContext(tasks, 8000))
		},
	})
}
