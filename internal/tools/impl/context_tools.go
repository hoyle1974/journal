package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
)

type listContextsArgs struct {
	Limit int `json:"limit" description:"Maximum number of contexts (default 10, max 20)" default:"10"`
}

type createContextArgs struct {
	Name        string `json:"name" description:"Short snake_case name for the context (e.g., 'party_planning', 'job_search')" required:"true"`
	Description string `json:"description" description:"Description of what this context is about" required:"true"`
	ContextType string `json:"context_type" description:"Type of context: 'permanent' (never decays) or 'auto' (decays over time)" enum:"permanent,auto"`
}

type touchContextArgs struct {
	Name  string  `json:"name" description:"Name of the context to touch" required:"true"`
	Boost float64 `json:"boost" description:"Relevance boost amount (0.0-0.5, default 0.1)" default:"0.1"`
}

type deleteContextArgs struct {
	ContextID string `json:"context_id" description:"The UUID of the context to delete" required:"true"`
}

type getProjectTimelineArgs struct {
	ProjectName string `json:"project_name" description:"Name of the project or goal (e.g. 'jot app', 'party planning')" required:"true"`
}

type updateProjectStatusArgs struct {
	ProjectName string `json:"project_name" description:"Name of the project/goal to update" required:"true"`
	Status      string `json:"status" description:"New status for the project" required:"true" enum:"active,blocked,completed,archived"`
}

func init() {
	registerContextTools()
	registerSystemEvolutionTools()
	registerProjectStatusTools()
}

func registerContextTools() {
	tools.Register(&tools.Tool{
		Name:        "list_contexts",
		Description: "List active contexts (ongoing projects, plans, and topics). Shows context name, type, relevance, and description.",
		Category:    "context",
		Args:        &listContextsArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*listContextsArgs)
			limit := clampInt(a.Limit, 10, 1, 20)
			contexts, metas, err := memory.GetActiveContexts(ctx, env, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if len(contexts) == 0 {
				return tools.OK("No active contexts found.")
			}
			result := formatContexts(contexts, metas)
			return tools.OK("Found %d active contexts:\n%s", len(contexts), result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "create_context",
		Description: "Manually create a new context for tracking an ongoing project, plan, or topic. Use this when the user explicitly wants to track something.",
		Category:    "context",
		Args:        &createContextArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*createContextArgs)
			if a.Name == "" {
				return tools.MissingParam("name")
			}
			if a.Description == "" {
				return tools.MissingParam("description")
			}
			contextType := a.ContextType
			if contextType == "" {
				contextType = "auto"
			}

			existing, _, err := memory.FindContextByName(ctx, env, a.Name)
			if err == nil && existing != nil {
				return tools.Fail("Context '%s' already exists.", a.Name)
			}

			var sourceEntries []string
			if cur := agent.CurrentEntryUUIDFrom(ctx); cur != "" {
				sourceEntries = []string{cur}
			}
			uuid, err := memory.CreateContext(ctx, env, a.Name, a.Description, contextType, nil, sourceEntries)
			if err != nil {
				return tools.Fail("Error creating context: %v", err)
			}
			return tools.OK("Context '%s' created successfully (ID: %s)", a.Name, uuid)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "touch_context",
		Description: "Update a context's relevance to mark it as recently active. Use when the user mentions an existing context.",
		Category:    "context",
		Args:        &touchContextArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*touchContextArgs)
			if a.Name == "" {
				return tools.MissingParam("name")
			}
			boost := a.Boost
			if boost < 0 {
				boost = 0
			}
			if boost > 0.5 {
				boost = 0.5
			}
			if boost == 0 {
				boost = 0.1
			}

			node, meta, err := memory.FindContextByName(ctx, env, a.Name)
			if err != nil || node == nil {
				return tools.Fail("Context '%s' not found.", a.Name)
			}

			var newSourceEntry *string
			if cur := agent.CurrentEntryUUIDFrom(ctx); cur != "" {
				newSourceEntry = &cur
			}
			err = memory.TouchContext(ctx, env, node.UUID, newSourceEntry, boost)
			if err != nil {
				return tools.Fail("Error touching context: %v", err)
			}
			newRelevance := meta.Relevance + boost
			if newRelevance > 1.0 {
				newRelevance = 1.0
			}
			return tools.OK("Context '%s' updated (new relevance: %.0f%%)", a.Name, newRelevance*100)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "delete_context",
		Description: "Delete a context by its UUID. Use to clean up duplicate or unwanted contexts.",
		Category:    "context",
		Args:        &deleteContextArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*deleteContextArgs)
			if a.ContextID == "" {
				return tools.MissingParam("context_id")
			}

			err := memory.DeleteContext(ctx, env, a.ContextID)
			if err != nil {
				return tools.Fail("Error deleting context: %v", err)
			}
			return tools.OK("Context deleted successfully.")
		},
	})
}

func registerSystemEvolutionTools() {
	tools.Register(&tools.Tool{
		Name:        "get_system_health_audit",
		Description: "Get the latest system evolution audit: recommended tool changes, knowledge gaps, and architectural suggestions from the Cognitive Engineer (nightly). Use when the user asks what to change, what's wrong with the system, or for improvement suggestions.",
		Category:    "context",
		Args:        &tools.NoArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			node, _, err := memory.FindContextByName(ctx, env, "system_evolution")
			if err != nil {
				return tools.Fail("Error finding system_evolution context: %v", err)
			}
			if node == nil {
				return tools.OK("No system evolution audit has been run yet. The nightly Dreamer will populate this after it runs.")
			}
			content := strings.TrimSpace(node.Content)
			if content == "" {
				return tools.OK("System evolution audit exists but is empty. The next nightly run will populate it.")
			}
			auditTs := journal.TruncateTimestamp(node.Timestamp, journal.DateTimeDisplayLen)
			if auditTs == "" {
				auditTs = "(no date)"
			}
			return tools.OK("System Evolution Audit (as of %s):\n\n%s", auditTs, content)
		},
	})
}

func registerProjectStatusTools() {
	tools.Register(&tools.Tool{
		Name:        "get_project_timeline",
		Description: "Get a unified timeline for a project: current status from knowledge, briefing content, and recent journal activity mentioning the project. Use when the user asks 'what's the status of X?', 'how's the jot app going?', or for a project summary with recent momentum. Limitation: 'recent activity' is the last calendar month only, not a rolling 30-day window.",
		Category:    "context",
		Args:        &getProjectTimelineArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*getProjectTimelineArgs)
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
			nodes, err := memory.QuerySimilarNodes(ctx, env, vec, 5)
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
			startStr, endStr, err := resolveToolDateRange("last month", "today")
			if err != nil {
				return tools.Fail("Date range error: %v", err)
			}
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			withAnalyses, err := journal.GetEntriesWithAnalysisByDateRange(ctx, client, startStr, endStr, 100)
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
				date := journal.TruncateTimestamp(ew.Entry.Timestamp, journal.DateDisplayLen)
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
		Category:    "context",
		Args:        &updateProjectStatusArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*updateProjectStatusArgs)
			if a.ProjectName == "" {
				return tools.MissingParam("project_name")
			}
			if a.Status == "" {
				return tools.MissingParam("status")
			}
			node, err := memory.FindProjectOrGoalByName(ctx, env, a.ProjectName)
			if err != nil {
				return tools.Fail("Error finding project: %v", err)
			}
			if node == nil {
				return tools.Fail("Project '%s' not found.", a.ProjectName)
			}
			if err := memory.UpdateProjectStatus(ctx, env, node.UUID, a.Status); err != nil {
				return tools.Fail("Failed to update status: %v", err)
			}
			return tools.OK("Project '%s' is now marked as %s.", a.ProjectName, a.Status)
		},
	})
}
