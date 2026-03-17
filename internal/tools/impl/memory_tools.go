package impl

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/service"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
)

type upsertKnowledgeArgs struct {
	Content  string `json:"content" description:"The fact or information to store (e.g., 'Alice works at Google')" required:"true"`
	NodeType string `json:"node_type" description:"Type of knowledge node" required:"true"`
	Metadata string `json:"metadata" description:"Optional JSON metadata. For project/goal use status one of: active, blocked, done, planning, pending, completed (e.g. {\"status\": \"active\"}). For person: relationship, occupation, etc."`
}

type semanticSearchArgs struct {
	Query      string `json:"query" description:"The natural language query to search for" required:"true"`
	Limit      int    `json:"limit" description:"Maximum number of results (default 10, max 20)" default:"10"`
	SourceText string `json:"source_text" description:"For debugging: the raw input used to build the query (set by code, not by assistant)"`
	Template   string `json:"template" description:"For debugging: the template used to build the query (e.g. 'Permanent facts about: {{.Input}}')"`
}

type listKnowledgeArgs struct {
	NodeType string `json:"node_type" description:"Filter by node type (leave empty for all types)"`
	Limit    int    `json:"limit" description:"Maximum number of results (default 20, max 50)" default:"20"`
}

type getEntityNetworkArgs struct {
	EntityName string `json:"entity_name" description:"Name or role of the person (e.g. 'wife', 'Gloria', 'Sarah')" required:"true"`
}

type generatePlanArgs struct {
	Goal string `json:"goal" description:"The goal to plan for" required:"true"`
}

type checkProactiveSignalsArgs struct {
	Limit int `json:"limit" description:"Maximum number of signals (default 5, max 10)" default:"5"`
}

func init() {
	registerKnowledgeTools()
	registerSignalTools()
}

func clampLimit(limit, def, min, max int) int {
	if limit == 0 {
		limit = def
	}
	if limit < min {
		return min
	}
	if limit > max {
		return max
	}
	return limit
}

func registerKnowledgeTools() {
	tools.Register(&tools.Tool{
		Name:        "upsert_knowledge",
		Description: "Add or update a piece of knowledge in the knowledge graph. Use ONLY for NEW facts in the CURRENT user input. NEVER upsert information from RECENT CONVERSATION - that data is already saved. Node types: 'person', 'project', 'fact', 'preference', 'list_item', 'goal', 'user_identity'. For node_type 'project' or 'goal', metadata.status must be exactly one of: active, blocked, done, planning, pending, completed (e.g. {\"status\": \"active\"}). Use node_type 'user_identity' for self-referential statements about your core identity (e.g. your name, role, values, traits); these are stored with high priority and are easily retrievable.",
		Category:    "knowledge",
		Args:        &upsertKnowledgeArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*upsertKnowledgeArgs)
			if a.Content == "" {
				return tools.MissingParam("content")
			}
			if a.NodeType == "" {
				return tools.MissingParam("node_type")
			}
			metadata := a.Metadata
			if metadata == "" {
				metadata = "{}"
			}
			var entryIDs []string
			if cur := agent.CurrentEntryUUIDFrom(ctx); cur != "" {
				entryIDs = []string{cur}
			}
			id, err := memory.UpsertKnowledge(ctx, env, a.Content, a.NodeType, metadata, entryIDs)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			return tools.OK("Knowledge node stored successfully (ID: %s)", id)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "semantic_search",
		Description: "Search the knowledge graph and journal entries using semantic similarity. Use this FIRST for questions about people, facts, preferences, or past journal content (who is X, where is X, what did I write about Y). When answering, include the source date when results show one (e.g. 'Buy ice [Source: 2026-02-15]').",
		Category:    "knowledge",
		Args:        &semanticSearchArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*semanticSearchArgs)
			if a.Query == "" {
				return tools.MissingParam("query")
			}
			limit := clampLimit(a.Limit, 10, 1, 20)
			logArgs := []interface{}{"query_preview", a.Query, "limit", limit, "reason", "vector+keyword search over knowledge and entries"}
			if a.SourceText != "" {
				logArgs = append(logArgs, "source_text", a.SourceText)
			}
			if a.Template != "" {
				logArgs = append(logArgs, "template", a.Template)
			}
			infra.LoggerFrom(ctx).Debug("semantic_search: starting", logArgs...)
			if env == nil || env.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			queryVec, err := infra.GenerateEmbedding(ctx, env.Config().GoogleCloudProject, a.Query)
			if err != nil {
				return tools.Fail("Error generating embedding: %v", err)
			}
			nodeLimit := (limit + 1) / 2
			entryLimit := limit / 2
			if entryLimit < 1 {
				entryLimit = 1
			}
			nodeCandidateLimit := nodeLimit * 3
			entryCandidateLimit := entryLimit * 3
			fusedNodeTopN := nodeLimit * 2

			var vectorNodes, keywordNodes []memory.KnowledgeNode
			var vectorEntries, keywordEntries []journal.Entry
			var nodeVecErr, nodeKwErr, entryVecErr, entryKwErr error
			var wg sync.WaitGroup
			wg.Add(4)
			go func() {
				defer wg.Done()
				vectorNodes, nodeVecErr = memory.QuerySimilarNodes(ctx, env, queryVec, nodeCandidateLimit)
			}()
			go func() {
				defer wg.Done()
				keywordNodes, nodeKwErr = memory.SearchKnowledgeNodes(ctx, env, a.Query, nodeCandidateLimit)
			}()
			go func() {
				defer wg.Done()
				vectorEntries, entryVecErr = journal.QuerySimilarEntries(ctx, client, queryVec, entryCandidateLimit)
			}()
			go func() {
				defer wg.Done()
				keywordEntries, entryKwErr = journal.SearchEntries(ctx, client, a.Query, entryCandidateLimit)
			}()
			wg.Wait()

			if nodeVecErr != nil {
				infra.LogVectorSearchFailed(ctx, "knowledge_nodes", nodeVecErr, 0)
				vectorNodes = nil
			}
			if nodeKwErr != nil {
				keywordNodes = nil
			}
			if nodeVecErr != nil && nodeKwErr != nil {
				return tools.Fail("Error: knowledge search failed (vector: %v; keyword: %v)", nodeVecErr, nodeKwErr)
			}
			if entryVecErr != nil {
				infra.LogVectorSearchFailed(ctx, "entries", entryVecErr, 0)
				vectorEntries = nil
			}
			if entryKwErr != nil {
				keywordEntries = nil
			}
			if entryVecErr != nil && entryKwErr != nil {
				return tools.Fail("Error: entries search failed (vector: %v; keyword: %v)", entryVecErr, entryKwErr)
			}

			fusedNodes := memory.FuseKnowledgeNodes(vectorNodes, keywordNodes, fusedNodeTopN)
			nodes, _ := memory.RerankNodes(ctx, env, a.Query, fusedNodes, nodeLimit)
			entries := memory.FuseEntries(vectorEntries, keywordEntries, entryLimit)

			if len(nodes) == 0 && len(entries) == 0 {
				return tools.OK("No semantic matches found for '%s'.", a.Query)
			}
			var parts []string
			if len(nodes) > 0 {
				parts = append(parts, "Knowledge:\n"+formatKnowledgeNodes(ctx, client, nodes))
			}
			if len(entries) > 0 {
				parts = append(parts, "Journal entries:\n"+formatEntries(entries))
			}
			total := len(nodes) + len(entries)
			return tools.OK("Found %d semantic matches for '%s':\n%s", total, a.Query, strings.Join(parts, "\n\n"))
		},
	})

	tools.Register(&tools.Tool{
		Name:        "list_knowledge",
		Description: "List knowledge nodes by type (person, project, fact, preference, user_identity, etc.).",
		Category:    "knowledge",
		Args:        &listKnowledgeArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*listKnowledgeArgs)
			nodeType := a.NodeType
			limit := clampLimit(a.Limit, 20, 1, 50)
			queryStr := "knowledge information facts"
			if nodeType != "" {
				queryStr = nodeType + " information"
			}
			if env == nil || env.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			queryVec, err := infra.GenerateEmbedding(ctx, env.Config().GoogleCloudProject, queryStr)
			if err != nil {
				return tools.Fail("Error generating embedding: %v", err)
			}
			nodes, err := memory.QuerySimilarNodes(ctx, env, queryVec, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if nodeType != "" {
				var filtered []memory.KnowledgeNode
				for _, n := range nodes {
					if n.NodeType == nodeType {
						filtered = append(filtered, n)
					}
				}
				nodes = filtered
			}
			if len(nodes) == 0 {
				if nodeType != "" {
					return tools.OK("No knowledge nodes found for type '%s'.", nodeType)
				}
				return tools.OK("No knowledge nodes found.")
			}
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			result := formatKnowledgeNodes(ctx, client, nodes)
			if nodeType != "" {
				return tools.OK("Found %d knowledge nodes of type '%s':\n%s", len(nodes), nodeType, result)
			}
			return tools.OK("Found %d knowledge nodes:\n%s", len(nodes), result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_entity_network",
		Description: "Get the entity profile and related knowledge. Dynamically discovers facts about the person even if not explicitly linked. Use for high-level questions like 'Who influenced me?', 'What are my wife's favorites?'.",
		Category:    "knowledge",
		Args:        &getEntityNetworkArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*getEntityNetworkArgs)
			entityName := strings.TrimSpace(a.EntityName)
			if entityName == "" {
				return tools.Fail("entity_name cannot be empty")
			}
			if env == nil || env.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			node, err := memory.FindEntityNodeByName(ctx, env, entityName)
			if err != nil {
				return tools.Fail("Error finding entity: %v", err)
			}
			if node == nil {
				return tools.OK("No profile found for '%s'. Try semantic_search for related facts.", entityName)
			}
			full, err := memory.GetKnowledgeNodeByID(ctx, env, node.UUID)
			if err != nil {
				return tools.Fail("Error loading entity: %v", err)
			}

			var discovered, linked []memory.KnowledgeNode
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				discovered, _ = memory.DiscoverRelatedNodes(ctx, env, a.EntityName, 10)
			}()
			go func() {
				defer wg.Done()
				if len(full.EntityLinks) > 0 {
					linked, _ = memory.GetKnowledgeNodesByIDs(ctx, env, full.EntityLinks)
				}
			}()
			wg.Wait()

			seen := make(map[string]bool)
			seen[full.UUID] = true
			var merged []memory.KnowledgeNode
			for _, n := range discovered {
				if n.UUID != "" && !seen[n.UUID] {
					seen[n.UUID] = true
					merged = append(merged, n)
				}
			}
			for _, n := range linked {
				if n.UUID != "" && !seen[n.UUID] {
					seen[n.UUID] = true
					merged = append(merged, n)
				}
			}

			var allParts []string
			allParts = append(allParts, fmt.Sprintf("PRIMARY: %s", full.Content))
			if full.Metadata != "" && full.Metadata != "{}" {
				allParts = append(allParts, fmt.Sprintf("PRIMARY_METADATA: %s", full.Metadata))
			}
			for _, n := range merged {
				allParts = append(allParts, fmt.Sprintf("FACT: %s", n.Content))
			}
			client, err := env.Firestore(ctx)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			for i, eid := range full.JournalEntryIDs {
				if i >= 5 {
					allParts = append(allParts, fmt.Sprintf("JOURNAL: ... and %d more entries", len(full.JournalEntryIDs)-5))
					break
				}
				e, err := journal.GetEntry(ctx, client, eid)
				if err != nil || e == nil {
					continue
				}
				content := e.Content
				if len(content) > 200 {
					content = content[:197] + "..."
				}
				entryTs := journal.TruncateTimestamp(e.Timestamp, journal.DateTimeDisplayLen)
				if entryTs == "" {
					entryTs = "(no date)"
				}
				allParts = append(allParts, fmt.Sprintf("JOURNAL: [%s] %s", entryTs, content))
			}
			allInfo := strings.Join(allParts, "\n")

			const synthesisPrompt = "Consolidate the following entity data into a concise profile. Remove redundant facts and merge overlapping information. Use bullets. Output only the profile, no preamble."
			userPrompt := utils.WrapAsUserData(allInfo)
			summary, err := infra.GenerateContentSimple(ctx, env, synthesisPrompt, userPrompt, env.Config(), &infra.GenConfig{MaxOutputTokens: 512})
			if err != nil {
				infra.LoggerFrom(ctx).Debug("get_entity_network synthesis failed, returning raw data", "error", err)
				return tools.OK("Entity: %s\n\n%s", a.EntityName, allInfo)
			}
			return tools.OK("Entity profile: %s\n\n%s", a.EntityName, summary)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "generate_plan",
		Description: "Generate a structured plan to achieve a goal. ONLY use when user explicitly says 'plan', 'help me plan', or 'create a plan for'. Breaks down the goal into phases and saves to knowledge graph.",
		Category:    "knowledge",
		Args:        &generatePlanArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*generatePlanArgs)
			if a.Goal == "" {
				return tools.MissingParam("goal")
			}
			if env == nil {
				return tools.Fail("No app in context")
			}
			result, err := service.CreateAndSavePlan(ctx, env, a.Goal)
			if err != nil {
				return tools.Fail("Error generating plan: %v", err)
			}
			return tools.OK("%s", result)
		},
	})
}

func registerSignalTools() {
	tools.Register(&tools.Tool{
		Name:        "check_proactive_signals",
		Description: "Check for background signals regarding stale goals, relationship health, or recurring user patterns. Use this if the user asks 'what should I focus on' or 'what am I forgetting'.",
		Category:    "knowledge",
		Args:        &checkProactiveSignalsArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*checkProactiveSignalsArgs)
			limit := clampLimit(a.Limit, 5, 1, 10)
			signals, err := memory.GetActiveSignals(ctx, env, limit)
			if err != nil {
				return tools.Fail("Error fetching signals: %v", err)
			}
			if signals == "" {
				return tools.OK("No proactive signals at this time.")
			}
			return tools.OK("Current Proactive Signals:\n%s", signals)
		},
	})
}
