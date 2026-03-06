package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
)

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
		Params: []tools.Param{
			tools.LimitParam(10, 20),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			limit := args.IntBounded("limit", 10, 1, 20)
			contexts, metas, err := memory.GetActiveContexts(ctx, limit)
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
		Params: []tools.Param{
			tools.RequiredStringParam("name", "Short snake_case name for the context (e.g., 'party_planning', 'job_search')"),
			tools.RequiredStringParam("description", "Description of what this context is about"),
			tools.EnumParam("context_type", "Type of context: 'permanent' (never decays) or 'auto' (decays over time)", false, []string{"permanent", "auto"}),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			name, ok := args.RequiredString("name")
			if !ok {
				return tools.MissingParam("name")
			}
			description, ok := args.RequiredString("description")
			if !ok {
				return tools.MissingParam("description")
			}
			contextType := args.String("context_type", "auto")

			existing, _, err := memory.FindContextByName(ctx, name)
			if err == nil && existing != nil {
				return tools.Fail("Context '%s' already exists.", name)
			}

			var sourceEntries []string
			if cur := agent.CurrentEntryUUIDFrom(ctx); cur != "" {
				sourceEntries = []string{cur}
			}
			uuid, err := memory.CreateContext(ctx, name, description, contextType, nil, sourceEntries)
			if err != nil {
				return tools.Fail("Error creating context: %v", err)
			}
			return tools.OK("Context '%s' created successfully (ID: %s)", name, uuid)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "touch_context",
		Description: "Update a context's relevance to mark it as recently active. Use when the user mentions an existing context.",
		Category:    "context",
		Params: []tools.Param{
			tools.RequiredStringParam("name", "Name of the context to touch"),
			tools.NumberParam("boost", "Relevance boost amount (0.0-0.5, default 0.1)", false),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			name, ok := args.RequiredString("name")
			if !ok {
				return tools.MissingParam("name")
			}
			boost := args.Float("boost", 0.1)
			if boost < 0 {
				boost = 0
			}
			if boost > 0.5 {
				boost = 0.5
			}

			node, meta, err := memory.FindContextByName(ctx, name)
			if err != nil || node == nil {
				return tools.Fail("Context '%s' not found.", name)
			}

			var newSourceEntry *string
			if cur := agent.CurrentEntryUUIDFrom(ctx); cur != "" {
				newSourceEntry = &cur
			}
			err = memory.TouchContext(ctx, node.UUID, newSourceEntry, boost)
			if err != nil {
				return tools.Fail("Error touching context: %v", err)
			}
			newRelevance := meta.Relevance + boost
			if newRelevance > 1.0 {
				newRelevance = 1.0
			}
			return tools.OK("Context '%s' updated (new relevance: %.0f%%)", name, newRelevance*100)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "delete_context",
		Description: "Delete a context by its UUID. Use to clean up duplicate or unwanted contexts.",
		Category:    "context",
		Params: []tools.Param{
			tools.RequiredStringParam("context_id", "The UUID of the context to delete"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			contextID, ok := args.RequiredString("context_id")
			if !ok {
				return tools.MissingParam("context_id")
			}

			err := memory.DeleteContext(ctx, contextID)
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
		Params:      nil,
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			node, _, err := memory.FindContextByName(ctx, "system_evolution")
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
		Params: []tools.Param{
			tools.RequiredStringParam("project_name", "Name of the project or goal (e.g. 'jot app', 'party planning')"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			projectName, ok := args.RequiredString("project_name")
			if !ok {
				return tools.MissingParam("project_name")
			}
			app := infra.GetApp(ctx)
			if app == nil || app.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			vec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, "Project: "+projectName)
			if err != nil {
				return tools.Fail("Error finding project: %v", err)
			}
			nodes, err := memory.QuerySimilarNodes(ctx, vec, 5)
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
			withAnalyses, err := journal.GetEntriesWithAnalysisByDateRange(ctx, startStr, endStr, 100)
			if err != nil {
				return tools.Fail("Error fetching journal entries: %v", err)
			}
			projectLower := strings.ToLower(projectName)
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
			b.WriteString(fmt.Sprintf("Project: %s\n", projectName))
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
		Params: []tools.Param{
			tools.RequiredStringParam("project_name", "Name of the project/goal to update"),
			tools.EnumParam("status", "New status for the project", true, []string{"active", "blocked", "completed", "archived"}),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			projectName, ok := args.RequiredString("project_name")
			if !ok {
				return tools.MissingParam("project_name")
			}
			status := args.String("status", "")
			if status == "" {
				return tools.MissingParam("status")
			}
			app := infra.GetApp(ctx)
			if app == nil || app.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			vec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, "Project: "+projectName)
			if err != nil {
				return tools.Fail("Error finding project: %v", err)
			}
			nodes, err := memory.QuerySimilarNodes(ctx, vec, 3)
			if err != nil || len(nodes) == 0 {
				return tools.Fail("Project '%s' not found.", projectName)
			}
			nodeID := nodes[0].UUID
			var meta map[string]interface{}
			if nodes[0].Metadata != "" {
				_ = json.Unmarshal([]byte(nodes[0].Metadata), &meta)
			}
			if meta == nil {
				meta = make(map[string]interface{})
			}
			meta["status"] = status
			metaJSON, _ := json.Marshal(meta)

			client, err := app.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error updating: %v", err)
			}
			_, err = client.Collection(memory.KnowledgeCollection).Doc(nodeID).Update(ctx, []firestore.Update{
				{Path: "metadata", Value: string(metaJSON)},
				{Path: "last_recalled_at", Value: time.Now().Format(time.RFC3339)},
			})
			if err != nil {
				return tools.Fail("Failed to update status: %v", err)
			}
			return tools.OK("Project '%s' is now marked as %s.", projectName, status)
		},
	})
}
