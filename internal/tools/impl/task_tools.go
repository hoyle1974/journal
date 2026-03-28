package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
)

type decomposeTaskArgs struct {
	TaskID string `json:"task_id" description:"UUID of the task to break down into subtasks" required:"true"`
}

type createTaskArgs struct {
	Content      string `json:"content" description:"Task description or title" required:"true"`
	ParentID     string `json:"parent_uuid" description:"Parent task UUID for hierarchy"`
	DueDate      string `json:"due_date" description:"Due date (YYYY-MM-DD)"`
	SystemPrompt string `json:"system_prompt" description:"Instructions for the LLM when working on this task"`
}

type getTaskArgs struct {
	TaskID string `json:"task_id" description:"Task UUID" required:"true"`
}

type updateTaskArgs struct {
	TaskID                string  `json:"task_id" description:"Task UUID" required:"true"`
	Content               string  `json:"content" description:"New task description/title"`
	ParentID              string  `json:"parent_uuid" description:"New parent task UUID, or empty to make root"`
	DueDate               string  `json:"due_date" description:"Due date (YYYY-MM-DD), or empty to clear"`
	SystemPrompt          string  `json:"system_prompt" description:"Instructions for the LLM when working on this task"`
	AddJournalEntryIDs    string  `json:"add_journal_entry_ids" description:"Comma-separated journal entry UUIDs to link to this task"`
	RemoveJournalEntryIDs string  `json:"remove_journal_entry_ids" description:"Comma-separated journal entry UUIDs to unlink from this task"`
	AddMemoryNodeIDs      string  `json:"add_memory_node_ids" description:"Comma-separated knowledge node UUIDs to link to this task"`
	RemoveMemoryNodeIDs   string  `json:"remove_memory_node_ids" description:"Comma-separated knowledge node UUIDs to unlink from this task"`
}

type updateTaskStatusArgs struct {
	TaskID    string `json:"task_id" description:"Task UUID" required:"true"`
	Status    string `json:"status" description:"New status" required:"true" enum:"pending,active,completed,abandoned"`
	Reasoning string `json:"reasoning" description:"Reason for the status change (required when completing or abandoning)"`
}

type searchTasksArgs struct {
	Query  string `json:"query" description:"Natural language search query; omit or leave empty to list open root-level tasks"`
	Limit  int    `json:"limit" description:"Maximum number of results (default 10, max 20)" default:"10"`
	Status string `json:"status" description:"Filter by status (pending, active, completed, abandoned)"`
}

type projectTimelineArgs struct {
	ProjectName string `json:"project_name" description:"Name of the project or goal (e.g. 'jot app', 'party planning')" required:"true"`
}

type updateProjectByNameArgs struct {
	ProjectName string `json:"project_name" description:"Name of the project/goal to update" required:"true"`
	Status      string `json:"status" description:"New status for the project" required:"true" enum:"active,blocked,completed,archived"`
}

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
		Args:        &createTaskArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*createTaskArgs)
			if a.Content == "" {
				return tools.MissingParam("content")
			}
			t := &memory.Task{
				Content:      a.Content,
				ParentID:     a.ParentID,
				DueDate:      a.DueDate,
				SystemPrompt: a.SystemPrompt,
				Status:       memory.TaskStatusPending,
			}
			if cur := agent.CurrentEntryUUIDFrom(ctx); cur != "" {
				t.JournalEntryIDs = []string{cur}
			}

			uuid, err := env.MemoryTasks().CreateTask(ctx, t)
			if err != nil {
				return tools.Fail("Error creating task: %v", err)
			}
			if a.DueDate != "" {
				return tools.OK("Task created (ID: %s, due: %s)", uuid, a.DueDate)
			}
			return tools.OK("Task created (ID: %s)", uuid)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_task",
		Description: "Get full details of a single task by ID (content, status, due_date, system_prompt, journal_entry_ids, memory_node_ids). Use when the user asks for due dates or details of a specific task, or before updating backlinks.",
		Category:    "task",
		Args:        &getTaskArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*getTaskArgs)
			if a.TaskID == "" {
				return tools.MissingParam("task_id")
			}
			t, err := env.MemoryTasks().GetTask(ctx, a.TaskID)
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
		Args:        &updateTaskArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*updateTaskArgs)
			if a.TaskID == "" {
				return tools.MissingParam("task_id")
			}
			opts := &memory.UpdateTaskOpts{}
			if a.Content != "" {
				opts.Content = &a.Content
			}
			if a.ParentID != "" {
				opts.ParentID = &a.ParentID
			}
			if a.DueDate != "" {
				opts.DueDate = &a.DueDate
			}
			if a.SystemPrompt != "" {
				opts.SystemPrompt = &a.SystemPrompt
			}
			if a.AddJournalEntryIDs != "" {
				opts.AddJournalEntryIDs = parseCommaSeparatedIDs(a.AddJournalEntryIDs)
			}
			if a.RemoveJournalEntryIDs != "" {
				opts.RemoveJournalEntryIDs = parseCommaSeparatedIDs(a.RemoveJournalEntryIDs)
			}
			if a.AddMemoryNodeIDs != "" {
				opts.AddMemoryNodeIDs = parseCommaSeparatedIDs(a.AddMemoryNodeIDs)
			}
			if a.RemoveMemoryNodeIDs != "" {
				opts.RemoveMemoryNodeIDs = parseCommaSeparatedIDs(a.RemoveMemoryNodeIDs)
			}
			hasEdit := opts.Content != nil || opts.ParentID != nil || opts.DueDate != nil || opts.SystemPrompt != nil ||
				len(opts.AddJournalEntryIDs) > 0 || len(opts.RemoveJournalEntryIDs) > 0 ||
				len(opts.AddMemoryNodeIDs) > 0 || len(opts.RemoveMemoryNodeIDs) > 0
			if !hasEdit {
				return tools.Fail("provide at least one field to update: content, parent_id, due_date, system_prompt, or add/remove journal/memory IDs")
			}
			err := env.MemoryTasks().UpdateTask(ctx, a.TaskID, opts)
			if err != nil {
				return tools.Fail("Error updating task: %v", err)
			}
			return tools.OK("Task %s updated", a.TaskID)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "update_task_status",
		Description: "Update a task's status. Use status: pending, active, completed, or abandoned. When marking completed or abandoned, reasoning is required and a reflection is saved to the journal.",
		Category:    "task",
		Args:        &updateTaskStatusArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*updateTaskStatusArgs)
			if a.TaskID == "" {
				return tools.MissingParam("task_id")
			}
			if a.Status == "" {
				return tools.MissingParam("status")
			}
			if (a.Status == memory.TaskStatusCompleted || a.Status == memory.TaskStatusAbandoned) && a.Reasoning == "" {
				return tools.Fail("reasoning is required when marking a task as completed or abandoned")
			}

			err := env.MemoryTasks().UpdateTaskStatus(ctx, a.TaskID, a.Status, a.Reasoning)
			if err != nil {
				return tools.Fail("Error updating task: %v", err)
			}
			return tools.OK("Task %s updated to %s", a.TaskID, a.Status)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "search_tasks",
		Description: "Search tasks by semantic similarity to the query, or list open root-level tasks. If query is empty or omitted, returns your open todo list roots (same as in context). Optionally filter by status (pending, active, completed, abandoned). Returns one line per task: uuid, status, due date (or 'not set'), and content. Use get_task for full details of a single task.",
		Category:    "task",
		Args:        &searchTasksArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*searchTasksArgs)
			query := strings.TrimSpace(a.Query)
			limit := clampInt(a.Limit, 10, 1, 20)
			statusFilter := a.Status

			var tasks []memory.Task
			var err error
			if query == "" {
				tasks, err = env.MemoryTasks().GetOpenRootTasks(ctx, limit*2)
				if err != nil {
					return tools.Fail("Error listing tasks: %v", err)
				}
			} else {
				if env == nil || env.Config() == nil {
					return tools.Fail("Error: no app in context")
				}
				vec, err := infra.GenerateEmbedding(ctx, env.Config().GoogleCloudProject, query, infra.EmbedTaskRetrievalQuery)
				if err != nil {
					return tools.Fail("Error generating embedding: %v", err)
				}
				tasks, err = env.MemoryStore().QuerySimilarTasks(ctx, vec, limit*2)
				if err != nil {
					return tools.Fail("Error searching tasks: %v", err)
				}
			}

			if statusFilter != "" {
				norm := memory.NormalizeTaskStatus(statusFilter) // pending, active, completed, abandoned
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
			return tools.OK("Found %d task(s):\n%s", len(tasks), memory.FormatTasksForContext(tasks, 8000))
		},
	})

	tools.Register(&tools.Tool{
		Name:        "decompose_task",
		Description: "Break a complex task or project into 3–7 smaller, actionable subtasks. Use proactively when the user mentions a large goal or asks 'how do I start' / 'help me plan'. Creates child tasks linked to the parent.",
		Category:    "task",
		Args:        &decomposeTaskArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*decomposeTaskArgs)
			if a.TaskID == "" {
				return tools.MissingParam("task_id")
			}
			if env == nil {
				return tools.Fail("No app in context")
			}
			result, err := env.MemoryTasks().BrainstormSubtasks(ctx, a.TaskID)
			if err != nil {
				return tools.Fail("Error decomposing task: %v", err)
			}
			return tools.OK("%s", result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_project_timeline",
		Description: "Get a unified timeline for a project: current status from knowledge, briefing content, and recent journal activity mentioning the project. Use when the user asks 'what's the status of X?', 'how's the jot app going?', or for a project summary with recent momentum. Limitation: 'recent activity' is the last calendar month only, not a rolling 30-day window.",
		Category:    "task",
		Args:        &projectTimelineArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*projectTimelineArgs)
			if a.ProjectName == "" {
				return tools.MissingParam("project_name")
			}
			if env == nil || env.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			vec, err := infra.GenerateEmbedding(ctx, env.Config().GoogleCloudProject, "Project: "+a.ProjectName)
			if err != nil {
				return tools.Fail("Error finding project: %v", err)
			}
			nodes, err := env.MemoryStore().QuerySimilarNodes(ctx, vec, 5)
			if err != nil {
				return tools.Fail("Error querying knowledge: %v", err)
			}
			var projectNode *memory.KnowledgeNode
			for i := range nodes {
				if nodes[i].NodeType == "project" || nodes[i].NodeType == "goal" {
					projectNode = &nodes[i]
					break
				}
			}
			startStr, endStr, err := utils.ResolveDateRange("last month", "today")
			if err != nil {
				return tools.Fail("Date range error: %v", err)
			}
			withAnalyses, err := env.MemoryStore().GetEntriesWithAnalysisByDateRange(ctx, startStr, endStr, 100)
			if err != nil {
				return tools.Fail("Error fetching journal entries: %v", err)
			}
			projectLower := strings.ToLower(a.ProjectName)
			var activityLines []string
			for _, ew := range withAnalyses {
				if ew.Analysis == nil {
					continue
				}
				var hasProject bool
				for _, e := range ew.Analysis.Entities {
					if strings.Contains(strings.ToLower(e.Name), projectLower) {
						hasProject = true
						break
					}
				}
				if !hasProject {
					continue
				}
				date := memory.TruncateTimestamp(ew.Entry.Timestamp, memory.DateDisplayLen)
				summary := ew.Analysis.Summary
				if summary == "" {
					summary = utils.TruncateString(ew.Entry.Content, 80)
				}
				activityLines = append(activityLines, fmt.Sprintf("- [%s] %s", date, summary))
			}
			var status, briefing string
			if projectNode != nil {
				var meta map[string]interface{}
				if projectNode.Metadata != "" {
					_ = json.Unmarshal([]byte(projectNode.Metadata), &meta)
				}
				if meta != nil {
					if s, _ := meta["status"].(string); s != "" {
						status = s
					}
				}
				if status == "" {
					status = "Unknown"
				}
				briefing = strings.TrimSpace(projectNode.Content)
			} else {
				status = "Not found in knowledge"
				briefing = "(No knowledge node for this project.)"
			}
			var b strings.Builder
			b.WriteString(fmt.Sprintf("Project: %s\n", a.ProjectName))
			b.WriteString(fmt.Sprintf("Current Status: %s\n", status))
			b.WriteString("Knowledge Briefing: ")
			b.WriteString(briefing)
			b.WriteString("\n\nRecent Activity:\n")
			if len(activityLines) == 0 {
				b.WriteString("(No journal entries in the last 30 days mentioning this project.)")
			} else {
				b.WriteString(strings.Join(activityLines, "\n"))
			}
			return tools.OK("%s", b.String())
		},
	})

	tools.Register(&tools.Tool{
		Name:        "update_project_status",
		Description: "Update the status of an ongoing goal or project (e.g. 'active', 'blocked', 'completed', 'archived'). Use this when the user indicates a project phase is done or finished.",
		Category:    "task",
		Args:        &updateProjectByNameArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*updateProjectByNameArgs)
			if a.ProjectName == "" {
				return tools.MissingParam("project_name")
			}
			if a.Status == "" {
				return tools.MissingParam("status")
			}
			node, err := env.MemoryStore().FindProjectOrGoalByName(ctx, a.ProjectName)
			if err != nil {
				return tools.Fail("Error finding project: %v", err)
			}
			if node == nil {
				return tools.Fail("Project '%s' not found.", a.ProjectName)
			}
			if err := env.MemoryKnowledge().UpdateProjectStatus(ctx, node.UUID, a.Status); err != nil {
				return tools.Fail("Failed to update status: %v", err)
			}
			return tools.OK("Project '%s' is now marked as %s.", a.ProjectName, a.Status)
		},
	})
}
