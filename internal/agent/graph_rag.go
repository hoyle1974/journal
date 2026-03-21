package agent

import (
	"context"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
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

// ExpandSearchResultsToSubgraph parses node UUIDs from a semantic_search result string,
// traverses 1 hop from each (capped at 3 seed nodes), and returns a combined Markdown
// subgraph for injection into the LLM's next turn.
// queryVector is the embedding of the user's query (from the semantic_search step).
// A nil queryVector disables semantic pruning (hard cap only).
func ExpandSearchResultsToSubgraph(ctx context.Context, env infra.ToolEnv, searchResult string, queryVector []float32) string {
	if ctx == nil || env == nil {
		return ""
	}
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
		sg, err := env.MemoryStore().GraphExpand(ctx, id, queryVector, 1, 8)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("graph_rag expand error", "uuid", id, "error", err)
			continue
		}
		parts = append(parts, sg.ToMarkdown(id))
	}

	if len(parts) == 0 {
		return ""
	}

	combined := strings.Join(parts, "\n\n")
	infra.LoggerFrom(ctx).Debug("graph_rag expanded context", "seeds", len(uuids), "results", len(parts))
	return combined
}
