package impl

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

// clampInt returns val clamped to [min, max], substituting def when val is 0.
// Used by tool implementations to normalise limit/count parameters.
func clampInt(val, def, min, max int) int {
	if val == 0 {
		val = def
	}
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

// resolveToolDateRange resolves start_date and end_date (natural language or YYYY-MM-DD) to YYYY-MM-DD strings for tool/DB use.
// Use this in all tools that accept date ranges (get_entries_by_date_range, get_queries_by_date, etc.) for consistent behavior.
func resolveToolDateRange(startExpr, endExpr string) (startStr, endStr string, err error) {
	return utils.ResolveDateRange(startExpr, endExpr)
}

const maxSourceDatesPerNode = 5
const maxEntryIDsToResolve = 25

// formatKnowledgeNodes formats knowledge nodes for LLM context, appending source dates when JournalEntryIDs are present.
// env is used for memory.GetEntryDates; pass the tool env at the call site.
func formatKnowledgeNodes(ctx context.Context, env infra.ToolEnv, nodes []memory.KnowledgeNode) string {
	// Collect unique entry IDs for batch date resolution
	seenIDs := make(map[string]bool)
	var allIDs []string
	for _, n := range nodes {
		for _, id := range n.JournalEntryIDs {
			if id != "" && !seenIDs[id] {
				seenIDs[id] = true
				allIDs = append(allIDs, id)
				if len(allIDs) >= maxEntryIDsToResolve {
					break
				}
			}
		}
		if len(allIDs) >= maxEntryIDsToResolve {
			break
		}
	}
	var dateMap map[string]string
	if env != nil {
		dateMap, _ = env.MemoryStore().GetEntryDates(ctx, allIDs)
	}

	var lines []string
	for i, n := range nodes {
		content := n.Content
		if len(content) > 200 {
			content = content[:197] + "..."
		}
		ts := memory.TruncateTimestamp(n.Timestamp, memory.DateTimeDisplayLen)
		if ts == "" {
			ts = "(no date)"
		}
		line := fmt.Sprintf("%d. [%s] [%s] %s", i+1, n.NodeType, ts, content)
		if len(n.JournalEntryIDs) > 0 && len(dateMap) > 0 {
			seenDate := make(map[string]bool)
			var dates []string
			for _, eid := range n.JournalEntryIDs {
				if d, ok := dateMap[eid]; ok && d != "" && !seenDate[d] {
					seenDate[d] = true
					dates = append(dates, d)
					if len(dates) >= maxSourceDatesPerNode {
						break
					}
				}
			}
			if len(dates) > 0 {
				line += fmt.Sprintf(" [Source: %s]", strings.Join(dates, ", "))
			}
		}
		lines = append(lines, line)
		if n.UUID != "" {
			lines = append(lines, fmt.Sprintf("   UUID: %s", n.UUID))
		}
		if n.Metadata != "" && n.Metadata != "{}" {
			lines = append(lines, fmt.Sprintf("   Metadata: %s", n.Metadata))
		}
	}
	return strings.Join(lines, "\n")
}

// formatQueriesForContext formats query history for LLM context using jot's formatter.
func formatQueriesForContext(queries []memory.QueryLog) string {
	return memory.FormatQueriesForContext(queries, 10000)
}

// filterEntriesWithImage returns entries that have an attached image (ImageURL != ""), up to maxN, preserving order.
func filterEntriesWithImage(entries []memory.Entry, maxN int) []memory.Entry {
	out := make([]memory.Entry, 0, maxN)
	for i := range entries {
		if entries[i].ImageURL != "" {
			out = append(out, entries[i])
			if len(out) >= maxN {
				break
			}
		}
	}
	return out
}
