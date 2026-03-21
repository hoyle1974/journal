package impl

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
)

type upsertKnowledgeArgs struct {
	Content     string `json:"content" description:"The fact or information to store (e.g., 'Alice works at Google')" required:"true"`
	NodeType    string `json:"node_type" description:"Type of knowledge node" required:"true"`
	Metadata    string `json:"metadata" description:"Optional JSON metadata. For project/goal use status one of: active, blocked, done, planning, pending, completed (e.g. {\"status\": \"active\"}). For person: relationship, occupation, etc."`
	Predicate   string `json:"predicate" description:"Optional: relationship predicate for relational facts stored as SPO triples (e.g. 'works_at', 'is_married_to', 'prefers'). Leave empty for non-relational facts."`
	ObjectValue string `json:"object_value" description:"Optional: the raw object string for SPO triple facts (e.g. 'Google', 'Gloria', 'dark chocolate'). Used together with predicate."`
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

type checkProactiveSignalsArgs struct {
	Limit int `json:"limit" description:"Maximum number of signals (default 5, max 10)" default:"5"`
}

func init() {
	registerKnowledgeTools()
	registerSignalTools()
	registerGraphTools()
}

func registerKnowledgeTools() {
	tools.Register(&tools.Tool{
		Name:        "upsert_knowledge",
		Description: "Add or update a piece of knowledge in the knowledge graph. Use ONLY for NEW facts in the CURRENT user input. NEVER upsert information from RECENT CONVERSATION - that data is already saved. Node types: 'person', 'project', 'fact', 'preference', 'list_item', 'goal', 'user_identity'. For node_type 'project' or 'goal', metadata.status must be exactly one of: active, blocked, done, planning, pending, completed (e.g. {\"status\": \"active\"}). Use node_type 'user_identity' for self-referential statements about your core identity (e.g. your name, role, values, traits); these are stored with high priority and are easily retrievable. Relational facts can be stored as SPO triples by supplying optional predicate (e.g. 'works_at') and object_value (e.g. 'Google') — these are persisted as graph edges alongside the content.",
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
			predicate := strings.TrimSpace(a.Predicate)
			if predicate != "" {
				predicate = memory.NormalizedPredicate(predicate)
				var spo *memory.SPOExtra
				spo = &memory.SPOExtra{
					Predicate:   predicate,
					ObjectValue: strings.TrimSpace(a.ObjectValue),
				}
				id, err := env.MemoryStore().UpsertSemanticMemoryPreembeddedWithSPO(ctx, a.Content, a.NodeType, "thought", 0.7, nil, entryIDs, nil, spo)
				if err != nil {
					return tools.Fail("Error: %v", err)
				}
				return tools.OK("Knowledge node stored successfully (ID: %s)", id)
			}
			id, err := env.MemoryStore().UpsertKnowledge(ctx, a.Content, a.NodeType, metadata, entryIDs)
			if err != nil {
				return tools.Fail("Error: %v", err)
			}
			return tools.OK("Knowledge node stored successfully (ID: %s)", id)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "semantic_search",
		Description: "Search semantic memory (high-significance facts) using vector similarity. Routes to knowledge nodes with significance_weight >= 0.7 — people, facts, preferences, projects, goals. Use this FIRST for factual questions (who is X, what do I prefer, what's the status of Y). For searching past events or log entries use search_entries instead. When answering, include the source date when results show one (e.g. 'Buy ice [Source: 2026-02-15]').",
		Category:    "knowledge",
		Args:        &semanticSearchArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*semanticSearchArgs)
			if a.Query == "" {
				return tools.MissingParam("query")
			}
			limit := clampInt(a.Limit, 10, 1, 20)
			logArgs := []interface{}{"query", a.Query, "limit", limit, "reason", "semantic search over unified journal (significance>=0.7)"}
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
			queryVec, err := infra.GenerateEmbedding(ctx, env.Config().GoogleCloudProject, a.Query)
			if err != nil {
				return tools.Fail("Error generating embedding: %v", err)
			}

			candidateLimit := limit * 3
			// Single-pass vector search: significance_weight >= 0.7 pre-filter routes directly to
			// semantic knowledge (Gold), excluding low-value gravel and raw log entries.
			const semanticMinSignificance = 0.7
			vectorNodes, vecErr := env.MemoryStore().QuerySimilarSemanticNodes(ctx, queryVec, candidateLimit, semanticMinSignificance)
			if vecErr != nil {
				infra.LogVectorSearchFailed(ctx, "journal(semantic)", vecErr, 0)
				vectorNodes = nil
			}
			// Keyword fallback on the same unified collection for exact-match safety.
			keywordNodes, _ := env.MemoryStore().SearchKnowledgeNodes(ctx, a.Query, candidateLimit)

			fusedNodes := memory.FuseKnowledgeNodes(vectorNodes, keywordNodes, limit*2)
			nodes, _ := env.MemoryStore().RerankNodes(ctx, a.Query, fusedNodes, limit)

			if vecErr != nil && len(nodes) == 0 {
				return tools.Fail("Error: semantic search failed (vector: %v)", vecErr)
			}
			if len(nodes) == 0 {
				return tools.OK("No semantic matches found for '%s'.", a.Query)
			}
			return tools.OK("Found %d semantic matches for '%s':\n%s", len(nodes), a.Query, formatKnowledgeNodes(ctx, env, nodes))
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
			limit := clampInt(a.Limit, 20, 1, 50)
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
			nodes, err := env.MemoryStore().QuerySimilarNodes(ctx, queryVec, limit)
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
			result := formatKnowledgeNodes(ctx, env, nodes)
			if nodeType != "" {
				return tools.OK("Found %d knowledge nodes of type '%s':\n%s", len(nodes), nodeType, result)
			}
			return tools.OK("Found %d knowledge nodes:\n%s", len(nodes), result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "get_entity_network",
		Description: "Get the entity profile and related knowledge, including 1-hop relationship edges (who relates to this entity and how). Dynamically discovers facts about the person even if not explicitly linked. Use for high-level questions like 'Who influenced me?', 'What are my wife's favorites?'.",
		Category:    "knowledge",
		Args:        &getEntityNetworkArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			ctx, span := infra.StartSpan(ctx, "tool.get_entity_network")
			defer span.End()

			a := args.(*getEntityNetworkArgs)
			entityName := strings.TrimSpace(a.EntityName)
			if entityName == "" {
				return tools.Fail("entity_name cannot be empty")
			}
			if env == nil || env.Config() == nil {
				return tools.Fail("Error: no app in context")
			}
			span.SetAttributes(map[string]string{"entity_name": entityName})

			node, err := env.MemoryStore().FindEntityNodeByName(ctx, entityName)
			if err != nil {
				return tools.Fail("Error finding entity: %v", err)
			}
			if node == nil {
				return tools.OK("No profile found for '%s'. Try semantic_search for related facts.", entityName)
			}
			full, err := env.MemoryStore().GetKnowledgeNodeByID(ctx, node.UUID)
			if err != nil {
				return tools.Fail("Error loading entity: %v", err)
			}
			infra.LoggerFrom(ctx).Debug("get_entity_network root node loaded", "uuid", full.UUID, "content", full.Content)

			// 1-hop traversal: run four lookups in parallel.
			var discovered, linked, incomingEdges, outgoingEdges []memory.KnowledgeNode
			var wg sync.WaitGroup
			wg.Add(4)
			go func() {
				defer wg.Done()
				discovered, _ = env.MemoryStore().DiscoverRelatedNodes(ctx, entityName, 10)
			}()
			go func() {
				defer wg.Done()
				if len(full.EntityLinks) > 0 {
					linked, _ = env.MemoryStore().GetKnowledgeNodesByIDs(ctx, full.EntityLinks)
				}
			}()
			go func() {
				defer wg.Done()
				// Incoming edges: nodes whose entity_links reference this entity's UUID.
				incomingEdges, _ = env.MemoryStore().QueryNodesLinkingTo(ctx, full.UUID, 20)
			}()
			go func() {
				defer wg.Done()
				// Outgoing SPO edges: relational nodes where this entity is the subject (object_uuid == root UUID).
				outgoingEdges, _ = env.MemoryStore().QueryOutgoingEdges(ctx, full.UUID, 20)
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
			for _, n := range incomingEdges {
				if n.UUID != "" && !seen[n.UUID] {
					seen[n.UUID] = true
					merged = append(merged, n)
				}
			}
			for _, n := range outgoingEdges {
				if n.UUID != "" && !seen[n.UUID] {
					seen[n.UUID] = true
					merged = append(merged, n)
				}
			}
			infra.LoggerFrom(ctx).Debug("get_entity_network 1-hop neighbors", "root_uuid", full.UUID, "discovered", len(discovered), "linked", len(linked), "incoming_edges", len(incomingEdges), "outgoing_edges", len(outgoingEdges), "merged_total", len(merged))

			var allParts []string
			allParts = append(allParts, fmt.Sprintf("PRIMARY: %s", full.Content))
			if full.Metadata != "" && full.Metadata != "{}" {
				allParts = append(allParts, fmt.Sprintf("PRIMARY_METADATA: %s", full.Metadata))
			}
			// Separate relational (SPO) nodes from plain facts for cleaner synthesis.
			for _, n := range merged {
				if n.Predicate != "" {
					allParts = append(allParts, fmt.Sprintf("RELATION: %s | %s | %s", full.Content, n.Predicate, n.Content))
				} else {
					allParts = append(allParts, fmt.Sprintf("FACT: %s", n.Content))
				}
			}
			for i, eid := range full.JournalEntryIDs {
				if i >= 5 {
					allParts = append(allParts, fmt.Sprintf("JOURNAL: ... and %d more entries", len(full.JournalEntryIDs)-5))
					break
				}
				e, err := env.MemoryStore().GetEntry(ctx, eid)
				if err != nil || e == nil {
					continue
				}
				entryContent := e.Content
				if len(entryContent) > 200 {
					entryContent = entryContent[:197] + "..."
				}
				entryTs := memory.TruncateTimestamp(e.Timestamp, memory.DateTimeDisplayLen)
				if entryTs == "" {
					entryTs = "(no date)"
				}
				allParts = append(allParts, fmt.Sprintf("JOURNAL: [%s] %s", entryTs, entryContent))
			}
			allInfo := strings.Join(allParts, "\n")
			infra.LoggerFrom(ctx).Debug("get_entity_network all_info assembled", "parts", len(allParts), "all_info", allInfo)

			const synthesisPrompt = `Consolidate the following entity data into a concise profile. Remove redundant facts and merge overlapping information.

Format the output as:
- Entity name and role
- Attributes (one bullet each)
- Relationships listed as "predicate: object" (one bullet each for RELATION lines)

Output only the profile, no preamble, no JSON.`
			userPrompt := utils.WrapAsUserData(allInfo)
			summary, err := infra.GenerateContentSimple(ctx, env, synthesisPrompt, userPrompt, env.Config(), &infra.GenConfig{MaxOutputTokens: 512})
			if err != nil {
				infra.LoggerFrom(ctx).Debug("get_entity_network synthesis failed, returning raw data", "error", err)
				return tools.OK("Entity: %s\n\n%s", entityName, allInfo)
			}
			return tools.OK("Entity profile: %s\n\n%s", entityName, summary)
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
			limit := clampInt(a.Limit, 5, 1, 10)
			signals, err := env.MemoryStore().GetActiveSignals(ctx, limit)
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
