package impl

import (
	"context"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/task"
	"github.com/jackstrohm/jot/tools"
)

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
			return tools.OK("Task created (ID: %s)", uuid)
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
		Description: "Search tasks by semantic similarity to the query. Optionally filter by status (pending, active, completed, abandoned). Returns tasks formatted for context.",
		Category:    "task",
		Params: []tools.Param{
			tools.RequiredStringParam("query", "Natural language search query"),
			tools.LimitParam(10, 20),
			tools.OptionalStringParam("status", "Filter by status (pending, active, completed, abandoned)"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			query, ok := args.RequiredString("query")
			if !ok {
				return tools.MissingParam("query")
			}
			limit := args.IntBounded("limit", 10, 1, 20)
			statusFilter := args.String("status", "")

			app := infra.GetApp(ctx)
			if app == nil || app.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			vec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, query, infra.EmbedTaskRetrievalDocument)
			if err != nil {
				return tools.Fail("Error generating embedding: %v", err)
			}

			tasks, err := task.QuerySimilarTasks(ctx, vec, limit*2)
			if err != nil {
				return tools.Fail("Error searching tasks: %v", err)
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
