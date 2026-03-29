package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
)

// LoomRAGContext holds the assembled 2-hop context for the Loom response worker.
type LoomRAGContext struct {
	SeedSource            string   // "spo" or "vector_fallback"
	SeedSummaries         []string // formatted lines for each seed node
	RelationshipSummaries []string // relationship nodes from 2-hop graph expansion
	HopNodeSummaries      []string // subject/object nodes and their hot_edges (1 more hop)
	SourceEntries         []string // deduplicated source journal entries referenced by graph nodes
}

// BuildLoomRAGContext performs 2-hop context retrieval from seed node IDs produced by the refinery.
// Uses two batch RPCs instead of N individual fetches:
//  1. Batch-fetch all seed nodes; classify as relationship or entity.
//  2. Collect second-hop IDs (subject/object for rels, EntityLinks for entities).
//  3. Batch-fetch second-hop nodes.
//  4. Always include open root tasks for task-aware reasoning.
//
// logContent is used as a keyword-search fallback when seedNodeIDs is empty (e.g. for questions
// that yield no extractable triples from the refinery).
func BuildLoomRAGContext(ctx context.Context, app *infra.App, logUUID, logContent string, seedNodeIDs []string) (*LoomRAGContext, error) {
	if app == nil {
		return nil, fmt.Errorf("BuildLoomRAGContext: app required")
	}
	result := &LoomRAGContext{}

	// Embed the log content once — used both for seed discovery (when no refinery seeds)
	// and for semantic pruning during graph expansion.
	var queryVec []float32
	if logContent != "" {
		vec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, logContent, infra.EmbedTaskRetrievalQuery)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("loom rag: embed content failed", "error", err)
		} else {
			queryVec = vec
		}
	}

	// When the refinery found no seeds (e.g. input is a question), fall back to a vector
	// search so graph context is always populated with semantically relevant nodes.
	if len(seedNodeIDs) == 0 && len(queryVec) > 0 {
		result.SeedSource = "vector_fallback"
		hits, err := app.MemoryGraph().QuerySimilar(ctx, queryVec, memory.SearchOptions{Limit: 20, MinSignificance: 0.5})
		if err != nil {
			infra.LoggerFrom(ctx).Warn("loom rag: vector fallback search failed", "error", err)
		}
		hits = memory.ApplyTemporalBias(hits, memory.TemporalDecayHalfLifeDays)
		if len(hits) > 10 {
			hits = hits[:10]
		}
		for _, n := range hits {
			seedNodeIDs = append(seedNodeIDs, n.UUID)
		}
	} else {
		result.SeedSource = "spo"
	}

	if len(seedNodeIDs) > 0 {
		sg, err := app.MemoryGraph().ExpandMulti(ctx, seedNodeIDs, queryVec, 2, 8)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("loom rag: graph expand failed", "error", err)
		} else {
			seenEdge := make(map[string]bool)
			seenEntryID := make(map[string]bool)
			var entryIDs []string

			// Collect entity nodes and relationships separately so we can sort before formatting.
			type entityItem struct {
				node   memory.KnowledgeNodeWithLinks
				uuid   string
				isSeed bool
			}
			type relItem struct {
				node memory.KnowledgeNodeWithLinks
			}
			var entityItems []entityItem
			var relItems []relItem

			for uuid, n := range sg.Nodes {
				// Collect journal entry IDs from every node.
				for _, eid := range n.JournalEntryIDs {
					if eid != "" && !seenEntryID[eid] {
						seenEntryID[eid] = true
						entryIDs = append(entryIDs, eid)
					}
				}
				if n.NodeType == memory.NodeTypeRelationship {
					relItems = append(relItems, relItem{node: n})
				} else if n.NodeType != memory.NodeTypeLog && n.NodeType != memory.NodeTypeResponse {
					entityItems = append(entityItems, entityItem{node: n, uuid: uuid, isSeed: sg.SeedUUIDs[uuid]})
				}
			}

			// Sort entity nodes by significance descending so the most relevant appear first.
			sort.Slice(entityItems, func(i, j int) bool {
				return entityItems[i].node.SignificanceWeight > entityItems[j].node.SignificanceWeight
			})
			for _, item := range entityItems {
				if item.isSeed {
					result.SeedSummaries = append(result.SeedSummaries, formatEntityNode(item.node))
				} else {
					result.HopNodeSummaries = append(result.HopNodeSummaries, formatEntityNode(item.node))
				}
			}

			// Sort relationships by relevance_score descending, then deduplicate.
			sort.Slice(relItems, func(i, j int) bool {
				return relItems[i].node.RelevanceScore > relItems[j].node.RelevanceScore
			})
			for _, item := range relItems {
				n := item.node
				subjectLabel := n.SubjectUUID
				if s, ok := sg.Nodes[n.SubjectUUID]; ok && s.Content != "" {
					subjectLabel = s.Content
				}
				objectLabel := n.ObjectUUID
				if o, ok := sg.Nodes[n.ObjectUUID]; ok && o.Content != "" {
					objectLabel = o.Content
				}
				key := subjectLabel + "|" + n.Predicate + "|" + objectLabel
				if seenEdge[key] {
					continue
				}
				seenEdge[key] = true
				result.RelationshipSummaries = append(result.RelationshipSummaries,
					formatRelNode(subjectLabel, n.Predicate, objectLabel, n.Timestamp))
			}

			// Fetch source journal entries in parallel and format them.
			if len(entryIDs) > 0 {
				type entryResult struct {
					id    string
					entry *memory.Entry
				}
				ch := make(chan entryResult, len(entryIDs))
				var wg sync.WaitGroup
				for _, eid := range entryIDs {
					wg.Add(1)
					go func(id string) {
						defer wg.Done()
						e, err := app.Memory.GetEntry(ctx, id)
						if err != nil || e == nil {
							ch <- entryResult{id: id}
							return
						}
						ch <- entryResult{id: id, entry: e}
					}(eid)
				}
				wg.Wait()
				close(ch)

				// Collect and sort by timestamp for stable output.
				type entrySummary struct {
					ts   string
					line string
				}
				var summaries []entrySummary
				for r := range ch {
					if r.entry == nil {
						continue
					}
					date := ""
					if len(r.entry.Timestamp) >= 10 {
						date = r.entry.Timestamp[:10]
					}
					preview := r.entry.Content
					if len(preview) > 120 {
						preview = preview[:117] + "..."
					}
					summaries = append(summaries, entrySummary{
						ts:   r.entry.Timestamp,
						line: fmt.Sprintf("[%s] %s: %s", r.id, date, preview),
					})
				}
				sort.Slice(summaries, func(i, j int) bool {
					return summaries[i].ts < summaries[j].ts
				})
				for _, s := range summaries {
					result.SourceEntries = append(result.SourceEntries, s.line)
				}
			}
		}
	}

	return result, nil
}

// FormatForPrompt returns a prompt-ready string block from the RAG context.
// Returns empty string if there is nothing to show.
func (r *LoomRAGContext) FormatForPrompt() string {
	if r.SeedSource == "" && len(r.SeedSummaries) == 0 && len(r.RelationshipSummaries) == 0 && len(r.HopNodeSummaries) == 0 && len(r.SourceEntries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Seeds (source: %s)\n", r.SeedSource))
	if len(r.SeedSummaries) > 0 {
		for _, s := range r.SeedSummaries {
			b.WriteString("- " + s + "\n")
		}
	} else {
		b.WriteString("(none)\n")
	}
	if len(r.RelationshipSummaries) > 0 {
		b.WriteString("## Related Graph Edges\n")
		for _, s := range r.RelationshipSummaries {
			b.WriteString("- " + s + "\n")
		}
	}
	if len(r.HopNodeSummaries) > 0 {
		b.WriteString("## Context Nodes\n")
		for _, s := range r.HopNodeSummaries {
			b.WriteString("- " + s + "\n")
		}
	}
	if len(r.SourceEntries) > 0 {
		b.WriteString("## Source Journal Entries\n")
		for _, s := range r.SourceEntries {
			b.WriteString("- " + s + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// formatRelNode formats a deduplicated edge as: subject -- predicate --> object [date].
func formatRelNode(subject, predicate, object, timestamp string) string {
	date := ""
	if len(timestamp) >= 10 {
		date = " [" + timestamp[:10] + "]"
	}
	return fmt.Sprintf("%s -- %s --> %s%s", subject, predicate, object, date)
}

// formatEntityNode formats an entity/concept node as: [type:uuid] content (links=N).
func formatEntityNode(n memory.KnowledgeNodeWithLinks) string {
	s := fmt.Sprintf("[%s:%s] %s", n.NodeType, n.UUID, n.Content)
	if len(n.EntityLinks) > 0 {
		s += fmt.Sprintf(" (links=%d)", len(n.EntityLinks))
	}
	return s
}

