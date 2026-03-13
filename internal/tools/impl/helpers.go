package impl

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

// resolveToolDateRange resolves start_date and end_date (natural language or YYYY-MM-DD) to YYYY-MM-DD strings for tool/DB use.
// Use this in all tools that accept date ranges (get_entries_by_date_range, get_queries_by_date, etc.) for consistent behavior.
func resolveToolDateRange(startExpr, endExpr string) (startStr, endStr string, err error) {
	return utils.ResolveDateRange(startExpr, endExpr)
}

const maxSourceDatesPerNode = 5
const maxEntryIDsToResolve = 25

// formatKnowledgeNodes formats knowledge nodes for LLM context, appending source dates when JournalEntryIDs are present.
func formatKnowledgeNodes(ctx context.Context, nodes []memory.KnowledgeNode) string {
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
	dateMap, _ := journal.GetEntryDates(ctx, allIDs)

	var lines []string
	for i, n := range nodes {
		content := n.Content
		if len(content) > 200 {
			content = content[:197] + "..."
		}
		ts := journal.TruncateTimestamp(n.Timestamp, journal.DateTimeDisplayLen)
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
		if n.Metadata != "" && n.Metadata != "{}" {
			lines = append(lines, fmt.Sprintf("   Metadata: %s", n.Metadata))
		}
	}
	return strings.Join(lines, "\n")
}

// formatEntries formats entries for LLM context (short form).
func formatEntries(entries []journal.Entry) string {
	var lines []string
	for i, e := range entries {
		content := e.Content
		if len(content) > 200 {
			content = content[:197] + "..."
		}
		ts := journal.TruncateTimestamp(e.Timestamp, journal.DateTimeDisplayLen)
		if ts == "" {
			ts = "(no date)"
		}
		src := ""
		if e.Source != "" {
			src = fmt.Sprintf(" (%s)", e.Source)
		}
		lines = append(lines, fmt.Sprintf("%d. [%s]%s %s", i+1, ts, src, content))
	}
	return strings.Join(lines, "\n")
}

// formatContexts formats context nodes and metadata for LLM context.
func formatContexts(nodes []memory.KnowledgeNode, metas []memory.ContextMetadata) string {
	var lines []string
	for i, n := range nodes {
		meta := metas[i]
		content := n.Content
		if len(content) > 150 {
			content = content[:147] + "..."
		}
		lastTouched := journal.TruncateTimestamp(meta.LastTouched, journal.DateTimeDisplayLen)
		if lastTouched == "" {
			lastTouched = "(no date)"
		}
		updated := journal.TruncateTimestamp(n.Timestamp, journal.DateTimeDisplayLen)
		if updated == "" {
			updated = "(no date)"
		}
		lines = append(lines, fmt.Sprintf("%d. [%s] %s (%.0f%% relevance)\n   UUID: %s\n   %s\n   Updated: %s | Last touched: %s",
			i+1, meta.ContextType, meta.ContextName, meta.Relevance*100, n.UUID, content, updated, lastTouched))
	}
	return strings.Join(lines, "\n\n")
}

// formatQueriesForContext formats query history for LLM context using jot's formatter.
func formatQueriesForContext(queries []journal.QueryLog) string {
	return journal.FormatQueriesForContext(queries, 10000)
}
