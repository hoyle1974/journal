package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/hoyle1974/memory"
)

// extractUUIDsFromSearchResult parses "   UUID: <id>" lines from formatKnowledgeNodes output.
// Deduplicates results. Returns nil if no UUID lines found.
func extractUUIDsFromSearchResult(result string) []string {
	seen := make(map[string]bool)
	var uuids []string
	for _, line := range strings.Split(result, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "UUID:") {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(trimmed, "UUID:"))
		if id != "" && !seen[id] {
			seen[id] = true
			uuids = append(uuids, id)
		}
	}
	return uuids
}

// formatGraphRAGContext renders a GraphExpandResult as a compact GRAPH CONTEXT block
// for automatic injection into the FOH context after semantic_search.
func formatGraphRAGContext(seedID string, r *memory.GraphExpandResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("GRAPH CONTEXT for %s [%s]:\n", r.Seed.Content, r.Seed.NodeType))
	writeNodes := func(label string, nodes []memory.KnowledgeNode) {
		if len(nodes) == 0 {
			return
		}
		sb.WriteString(label + ":\n")
		for _, n := range nodes {
			line := fmt.Sprintf("  [%s] %s", n.NodeType, n.Content)
			if n.Predicate != "" {
				line += fmt.Sprintf(" (%s)", n.Predicate)
			}
			sb.WriteString(line + "\n")
		}
	}
	writeNodes("Related (outgoing SPO)", r.Outgoing)
	writeNodes("Referenced by", r.Incoming)
	writeNodes("Linked entities", r.Linked)
	return strings.TrimRight(sb.String(), "\n")
}

// ExpandSearchResultsToSubgraph parses node UUIDs from a semantic_search result string,
// traverses 1 hop from each (capped at 3 seed nodes), and returns a combined GRAPH CONTEXT
// block for injection into the LLM's next turn. Returns empty string if no UUIDs found or
// all traversals fail.
func ExpandSearchResultsToSubgraph(ctx context.Context, env infra.ToolEnv, searchResult string) string {
	ctx, span := infra.StartSpan(ctx, "agent.graph_rag_expand")
	defer span.End()

	uuids := extractUUIDsFromSearchResult(searchResult)
	if len(uuids) == 0 {
		return ""
	}
	if len(uuids) > 3 {
		uuids = uuids[:3]
	}

	var parts []string
	for _, id := range uuids {
		result, err := env.MemoryStore().GraphExpand(ctx, id, 1, 8)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("graph_rag expand error", "uuid", id, "error", err)
			continue
		}
		parts = append(parts, formatGraphRAGContext(id, result))
	}

	if len(parts) == 0 {
		return ""
	}

	combined := strings.Join(parts, "\n\n")
	infra.LoggerFrom(ctx).Debug("graph_rag expanded context", "seeds", len(uuids), "results", len(parts))
	return combined
}
