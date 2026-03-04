package impl

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackstrohm/jot"
	"github.com/jackstrohm/jot/tools"
)

func init() {
	registerKnowledgeTools()
	registerSignalTools()
}

func registerKnowledgeTools() {
	tools.Register(&tools.Tool{
		Name:        "upsert_knowledge",
		Description: "Add or update a piece of knowledge in the knowledge graph. Use ONLY for NEW facts in the CURRENT user input. NEVER upsert information from RECENT HISTORY or RECENT CONVERSATION - that data is already saved. Node types: 'person', 'project', 'fact', 'preference', 'list_item', 'goal'.",
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
			id, err := jot.UpsertKnowledge(ctx, content, nodeType, metadata)
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
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			query, ok := args.RequiredString("query")
			if !ok {
				return tools.MissingParam("query")
			}
			limit := args.IntBounded("limit", 10, 1, 20)
			queryVec, err := jot.GenerateEmbedding(ctx, query)
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

			var vectorNodes, keywordNodes []jot.KnowledgeNode
			var vectorEntries, keywordEntries []jot.Entry
			var nodeVecErr, nodeKwErr, entryVecErr, entryKwErr error
			var wg sync.WaitGroup
			wg.Add(4)
			go func() {
				defer wg.Done()
				vectorNodes, nodeVecErr = jot.QuerySimilarNodes(ctx, queryVec, nodeCandidateLimit)
			}()
			go func() {
				defer wg.Done()
				keywordNodes, nodeKwErr = jot.SearchKnowledgeNodes(ctx, query, nodeCandidateLimit)
			}()
			go func() {
				defer wg.Done()
				vectorEntries, entryVecErr = jot.QuerySimilarEntries(ctx, queryVec, entryCandidateLimit)
			}()
			go func() {
				defer wg.Done()
				keywordEntries, entryKwErr = jot.SearchEntries(ctx, query, entryCandidateLimit)
			}()
			wg.Wait()

			if nodeVecErr != nil {
				vectorNodes = nil
			}
			if nodeKwErr != nil {
				keywordNodes = nil
			}
			if nodeVecErr != nil && nodeKwErr != nil {
				return tools.Fail("Error: knowledge search failed (vector: %v; keyword: %v)", nodeVecErr, nodeKwErr)
			}
			if entryVecErr != nil {
				vectorEntries = nil
			}
			if entryKwErr != nil {
				keywordEntries = nil
			}
			if entryVecErr != nil && entryKwErr != nil {
				return tools.Fail("Error: entries search failed (vector: %v; keyword: %v)", entryVecErr, entryKwErr)
			}

			fusedNodes := jot.FuseKnowledgeNodes(vectorNodes, keywordNodes, fusedNodeTopN)
			nodes, _ := jot.RerankNodes(ctx, query, fusedNodes, nodeLimit)
			entries := jot.FuseEntries(vectorEntries, keywordEntries, entryLimit)

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
		Description: "List knowledge nodes by type (person, project, fact, preference, etc.).",
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
			queryVec, err := jot.GenerateEmbedding(ctx, queryStr)
			if err != nil {
				return tools.Fail("Error generating embedding: %v", err)
			}
			nodes, err := jot.QuerySimilarNodes(ctx, queryVec, limit)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			if nodeType != "" {
				var filtered []jot.KnowledgeNode
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
		Description: "Get the entity profile and first-degree related nodes for a person (e.g. wife, Gloria). Use for high-level questions like 'Who influenced me?', 'What are my wife's favorites?'. Returns the entity node plus linked facts and people.",
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
			node, err := jot.FindEntityNodeByName(ctx, entityName)
			if err != nil {
				return tools.Fail("Error finding entity: %v", err)
			}
			if node == nil {
				return tools.OK("No entity found for '%s'. Try semantic_search for related facts.", entityName)
			}
			full, err := jot.GetKnowledgeNodeByID(ctx, node.UUID)
			if err != nil {
				return tools.Fail("Error loading entity: %v", err)
			}
			var parts []string
			entityTs := full.Timestamp
			if len(entityTs) > 19 {
				entityTs = entityTs[:19]
			}
			if entityTs == "" {
				entityTs = "(no date)"
			}
			parts = append(parts, fmt.Sprintf("Entity: [%s] [%s] %s", full.NodeType, entityTs, full.Content))
			if full.Metadata != "" && full.Metadata != "{}" {
				parts = append(parts, fmt.Sprintf("Metadata: %s", full.Metadata))
			}
			if len(full.EntityLinks) > 0 {
				related, err := jot.GetKnowledgeNodesByIDs(ctx, full.EntityLinks)
				if err != nil {
					parts = append(parts, fmt.Sprintf("Related (fetch error: %v)", err))
				} else if len(related) > 0 {
					parts = append(parts, "Related (first-degree):\n"+formatKnowledgeNodes(ctx, related))
				}
			}
			if len(full.JournalEntryIDs) > 0 {
				var entryLines []string
				for i, eid := range full.JournalEntryIDs {
					if i >= 5 {
						entryLines = append(entryLines, fmt.Sprintf("... and %d more entries", len(full.JournalEntryIDs)-5))
						break
					}
					e, err := jot.GetEntry(ctx, eid)
					if err != nil || e == nil {
						continue
					}
					content := e.Content
					if len(content) > 120 {
						content = content[:117] + "..."
					}
					entryTs := e.Timestamp
					if len(entryTs) > 19 {
						entryTs = entryTs[:19]
					}
					if entryTs == "" {
						entryTs = "(no date)"
					}
					entryLines = append(entryLines, fmt.Sprintf("- [%s] %s", entryTs, content))
				}
				if len(entryLines) > 0 {
					parts = append(parts, "From journal:\n"+strings.Join(entryLines, "\n"))
				}
			}
			return tools.OK("%s", strings.Join(parts, "\n\n"))
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
			result, err := jot.CreateAndSavePlan(ctx, goal)
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
			signals, err := jot.GetActiveSignals(ctx, limit)
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
