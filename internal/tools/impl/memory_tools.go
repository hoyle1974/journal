package impl

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackstrohm/jot/internal/service"
	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
)

func init() {
	registerKnowledgeTools()
	registerSignalTools()
}

func registerKnowledgeTools() {
	tools.Register(&tools.Tool{
		Name:        "upsert_knowledge",
		Description: "Add or update a piece of knowledge in the knowledge graph. Use ONLY for NEW facts in the CURRENT user input. NEVER upsert information from RECENT CONVERSATION - that data is already saved. Node types: 'person', 'project', 'fact', 'preference', 'list_item', 'goal', 'user_identity'. Use node_type 'user_identity' for self-referential statements about your core identity (e.g. your name, role, values, traits); these are stored with high priority and are easily retrievable.",
		Category:    "knowledge",
		Params: []tools.Param{
			tools.RequiredStringParam("content", "The fact or information to store (e.g., 'Alice works at Google')"),
			tools.RequiredStringParam("node_type", "Type of knowledge node"),
			tools.OptionalStringParam("metadata", "Optional JSON metadata (e.g., {\"relationship\": \"wife\", \"name\": \"Sarah\"})"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			content, ok := args.RequiredString("content")
			if !ok {
				return tools.MissingParam("content")
			}
			nodeType, ok := args.RequiredString("node_type")
			if !ok {
				return tools.MissingParam("node_type")
			}
			metadata := args.String("metadata", "{}")
			var entryIDs []string
			if cur := agent.CurrentEntryUUIDFrom(ctx); cur != "" {
				entryIDs = []string{cur}
			}
			id, err := memory.UpsertKnowledge(ctx, content, nodeType, metadata, entryIDs)
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
		Params: []tools.Param{
			tools.RequiredStringParam("query", "The natural language query to search for"),
			tools.LimitParam(10, 20),
			tools.OptionalStringParam("source_text", "For debugging: the raw input used to build the query (set by code, not by assistant)"),
			tools.OptionalStringParam("template", "For debugging: the template used to build the query (e.g. 'Permanent facts about: {{.Input}}')"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			query, ok := args.RequiredString("query")
			if !ok {
				return tools.MissingParam("query")
			}
			limit := args.IntBounded("limit", 10, 1, 20)
			sourceText := args.String("source_text", "")
			template := args.String("template", "")
			logArgs := []interface{}{"query_preview", query, "limit", limit, "reason", "vector+keyword search over knowledge and entries"}
			if sourceText != "" {
				logArgs = append(logArgs, "source_text", sourceText)
			}
			if template != "" {
				logArgs = append(logArgs, "template", template)
			}
			infra.LoggerFrom(ctx).Debug("semantic_search: starting", logArgs...)
			app := infra.GetApp(ctx)
			if app == nil || app.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			queryVec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, query)
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
				vectorNodes, nodeVecErr = memory.QuerySimilarNodes(ctx, queryVec, nodeCandidateLimit)
			}()
			go func() {
				defer wg.Done()
				keywordNodes, nodeKwErr = memory.SearchKnowledgeNodes(ctx, query, nodeCandidateLimit)
			}()
			go func() {
				defer wg.Done()
				vectorEntries, entryVecErr = journal.QuerySimilarEntries(ctx, queryVec, entryCandidateLimit)
			}()
			go func() {
				defer wg.Done()
				keywordEntries, entryKwErr = journal.SearchEntries(ctx, query, entryCandidateLimit)
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
			nodes, _ := memory.RerankNodes(ctx, query, fusedNodes, nodeLimit)
			entries := memory.FuseEntries(vectorEntries, keywordEntries, entryLimit)

			if len(nodes) == 0 && len(entries) == 0 {
				return tools.OK("No semantic matches found for '%s'.", query)
			}
			var parts []string
			if len(nodes) > 0 {
				parts = append(parts, "Knowledge:\n"+formatKnowledgeNodes(ctx, nodes))
			}
			if len(entries) > 0 {
				parts = append(parts, "Journal entries:\n"+formatEntries(entries))
			}
			total := len(nodes) + len(entries)
			return tools.OK("Found %d semantic matches for '%s':\n%s", total, query, strings.Join(parts, "\n\n"))
		},
	})

	tools.Register(&tools.Tool{
		Name:        "list_knowledge",
		Description: "List knowledge nodes by type (person, project, fact, preference, user_identity, etc.).",
		Category:    "knowledge",
		Params: []tools.Param{
			tools.OptionalStringParam("node_type", "Filter by node type (leave empty for all types)"),
			tools.LimitParam(20, 50),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			nodeType := args.String("node_type", "")
			limit := args.IntBounded("limit", 20, 1, 50)
			queryStr := "knowledge information facts"
			if nodeType != "" {
				queryStr = nodeType + " information"
			}
			app := infra.GetApp(ctx)
			if app == nil || app.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			queryVec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, queryStr)
			if err != nil {
				return tools.Fail("Error generating embedding: %v", err)
			}
			nodes, err := memory.QuerySimilarNodes(ctx, queryVec, limit)
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
			result := formatKnowledgeNodes(ctx, nodes)
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
		Params: []tools.Param{
			tools.RequiredStringParam("entity_name", "Name or role of the person (e.g. 'wife', 'Gloria', 'Sarah')"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			entityName, ok := args.RequiredString("entity_name")
			if !ok {
				return tools.MissingParam("entity_name")
			}
			entityName = strings.TrimSpace(entityName)
			if entityName == "" {
				return tools.Fail("entity_name cannot be empty")
			}
			app := infra.GetApp(ctx)
			if app == nil || app.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			node, err := memory.FindEntityNodeByName(ctx, entityName)
			if err != nil {
				return tools.Fail("Error finding entity: %v", err)
			}
			if node == nil {
				return tools.OK("No profile found for '%s'. Try semantic_search for related facts.", entityName)
			}
			full, err := memory.GetKnowledgeNodeByID(ctx, node.UUID)
			if err != nil {
				return tools.Fail("Error loading entity: %v", err)
			}

			var discovered, linked []memory.KnowledgeNode
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				discovered, _ = memory.DiscoverRelatedNodes(ctx, entityName, 10)
			}()
			go func() {
				defer wg.Done()
				if len(full.EntityLinks) > 0 {
					linked, _ = memory.GetKnowledgeNodesByIDs(ctx, full.EntityLinks)
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
			for i, eid := range full.JournalEntryIDs {
				if i >= 5 {
					allParts = append(allParts, fmt.Sprintf("JOURNAL: ... and %d more entries", len(full.JournalEntryIDs)-5))
					break
				}
				e, err := journal.GetEntry(ctx, eid)
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
			summary, err := infra.GenerateContentSimple(ctx, synthesisPrompt, userPrompt, app.Config(), &infra.GenConfig{MaxOutputTokens: 512})
			if err != nil {
				infra.LoggerFrom(ctx).Debug("get_entity_network synthesis failed, returning raw data", "error", err)
				return tools.OK("Entity: %s\n\n%s", entityName, allInfo)
			}
			return tools.OK("Entity profile: %s\n\n%s", entityName, summary)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "generate_plan",
		Description: "Generate a structured plan to achieve a goal. ONLY use when user explicitly says 'plan', 'help me plan', or 'create a plan for'. Breaks down the goal into phases and saves to knowledge graph.",
		Category:    "knowledge",
		Params: []tools.Param{
			tools.RequiredStringParam("goal", "The goal to plan for"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			goal, ok := args.RequiredString("goal")
			if !ok {
				return tools.MissingParam("goal")
			}
			app := infra.GetApp(ctx)
			if app == nil {
				return tools.Fail("No app in context")
			}
			result, err := service.CreateAndSavePlan(ctx, app, goal)
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
		Params:      []tools.Param{tools.LimitParam(5, 10)},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			limit := args.IntBounded("limit", 5, 1, 10)
			signals, err := memory.GetActiveSignals(ctx, limit)
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
