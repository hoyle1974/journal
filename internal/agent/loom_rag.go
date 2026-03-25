package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
)

// LoomRAGContext holds the assembled 2-hop context for the Loom response worker.
type LoomRAGContext struct {
	RelationshipSummaries []string // top-5 relationship nodes similar to the log entry
	HopNodeSummaries      []string // subject/object nodes and their hot_edges (1 more hop)
	PendingTaskSummaries  []string // pending/active tasks for additional context
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
	ctx, span := infra.StartSpan(ctx, "loom.build_rag_context")
	defer span.End()

	result := &LoomRAGContext{}

	// When the refinery found no seeds (e.g. input is a question), fall back to a keyword
	// search on the content terms so graph context is always populated.
	// Strip stop/question words first — SearchKnowledgeNodes requires ALL words to match,
	// so "Who is Alex" would require nodes to contain "who", "is", and "alex".
	// Replace first-person pronouns with the owner's name so "my goals" → "Alex goals".
	if len(seedNodeIDs) == 0 && logContent != "" {
		ownerName, _ := app.Memory.FetchOwnerName(ctx)
		if terms := keywordTerms(logContent, ownerName); terms != "" {
			hits, err := app.MemoryGraph().SearchKeywords(ctx, terms, 10)
			if err != nil {
				infra.LoggerFrom(ctx).Warn("loom rag: keyword fallback search failed", "error", err)
			}
			for _, n := range hits {
				seedNodeIDs = append(seedNodeIDs, n.UUID)
			}
		}
	}

	if len(seedNodeIDs) > 0 {
		sg, err := app.MemoryGraph().ExpandMulti(ctx, seedNodeIDs, nil, 2, 8)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("loom rag: graph expand failed", "error", err)
		} else {
			for uuid, n := range sg.Nodes {
				if n.NodeType == memory.NodeTypeRelationship {
					result.RelationshipSummaries = append(result.RelationshipSummaries,
						formatRelNode(n, sg.Nodes))
				} else if n.NodeType != "log" && n.NodeType != "response" {
					seed := sg.SeedUUIDs[uuid]
					result.HopNodeSummaries = append(result.HopNodeSummaries,
						formatEntityNode(n, seed))
				}
			}
		}
	}

	// Always include open tasks for task-aware reasoning.
	tasks, err := app.Memory.GetOpenRootTasks(ctx, 10)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("loom rag: fetch open tasks failed", "error", err)
	}
	for _, t := range tasks {
		result.PendingTaskSummaries = append(result.PendingTaskSummaries,
			fmt.Sprintf("[task] %s | status=%s | %s", t.UUID, t.Status, t.Content))
	}
	return result, nil
}

// FormatForPrompt returns a prompt-ready string block from the RAG context.
func (r *LoomRAGContext) FormatForPrompt() string {
	var b strings.Builder
	if len(r.RelationshipSummaries) > 0 {
		b.WriteString("## Related Graph Edges\n")
		for _, s := range r.RelationshipSummaries {
			b.WriteString("- " + s + "\n")
		}
	}
	if len(r.HopNodeSummaries) > 0 {
		b.WriteString("\n## Expanded Context Nodes\n")
		for _, s := range r.HopNodeSummaries {
			b.WriteString("- " + s + "\n")
		}
	}
	if len(r.PendingTaskSummaries) > 0 {
		b.WriteString("\n## Open Tasks\n")
		for _, s := range r.PendingTaskSummaries {
			b.WriteString("- " + s + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// stopWords are common English words and question words that add no search signal.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
	"with": true, "by": true, "from": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true, "has": true,
	"had": true, "do": true, "does": true, "did": true, "will": true, "would": true,
	"could": true, "should": true, "may": true, "might": true, "can": true,
	"who": true, "what": true, "when": true, "where": true, "why": true, "how": true,
	"which": true, "that": true, "this": true, "these": true, "those": true,
	"i": true, "me": true, "my": true, "we": true, "our": true, "you": true,
	"your": true, "he": true, "she": true, "it": true, "they": true, "their": true,
	"tell": true, "about": true, "know": true, "get": true,
}

// firstPersonPronouns are replaced with the owner's name when known.
var firstPersonPronouns = map[string]bool{
	"i": true, "me": true, "my": true, "mine": true, "myself": true,
}

// formatRelNode formats a relationship node with full SPO data and resolved subject/object labels.
func formatRelNode(n memory.KnowledgeNodeWithLinks, nodes map[string]memory.KnowledgeNodeWithLinks) string {
	subjectLabel := n.SubjectUUID
	if s, ok := nodes[n.SubjectUUID]; ok && s.Content != "" {
		subjectLabel = s.Content
	}
	objectLabel := n.ObjectUUID
	if o, ok := nodes[n.ObjectUUID]; ok && o.Content != "" {
		objectLabel = o.Content
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[rel:%s] %s --%s--> %s", n.UUID, subjectLabel, n.Predicate, objectLabel)
	if n.Metadata != "" && n.Metadata != "{}" {
		fmt.Fprintf(&b, " | meta=%s", n.Metadata)
	}
	if n.RelevanceScore > 0 {
		fmt.Fprintf(&b, " | score=%.2f", n.RelevanceScore)
	}
	if n.LogicTrace != "" {
		fmt.Fprintf(&b, " | trace=%s", n.LogicTrace)
	}
	if n.Timestamp != "" {
		fmt.Fprintf(&b, " | ts=%s", n.Timestamp)
	}
	return b.String()
}

// formatEntityNode formats an entity/concept node with all available fields.
func formatEntityNode(n memory.KnowledgeNodeWithLinks, isSeed bool) string {
	var b strings.Builder
	seedMark := ""
	if isSeed {
		seedMark = "*"
	}
	fmt.Fprintf(&b, "[%s%s:%s] %s", seedMark, n.NodeType, n.UUID, n.Content)
	if n.Metadata != "" && n.Metadata != "{}" {
		fmt.Fprintf(&b, " | meta=%s", n.Metadata)
	}
	if n.RelevanceScore > 0 {
		fmt.Fprintf(&b, " | score=%.2f", n.RelevanceScore)
	}
	if len(n.HotEdges) > 0 {
		fmt.Fprintf(&b, " | hot_edges=%s", strings.Join(n.HotEdges, ","))
	}
	if len(n.EntityLinks) > 0 {
		fmt.Fprintf(&b, " | links=%d", len(n.EntityLinks))
	}
	if n.LogicTrace != "" {
		fmt.Fprintf(&b, " | trace=%s", n.LogicTrace)
	}
	if n.Timestamp != "" {
		fmt.Fprintf(&b, " | ts=%s", n.Timestamp)
	}
	return b.String()
}

// keywordTerms strips stop words from s and returns the remaining words joined by spaces.
// First-person pronouns are replaced with ownerName when non-empty.
// Returns "" if no meaningful terms remain.
func keywordTerms(s, ownerName string) string {
	words := strings.Fields(strings.ToLower(s))
	out := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.TrimRight(w, "?.,!;:")
		if w == "" {
			continue
		}
		if firstPersonPronouns[w] {
			if ownerName != "" {
				out = append(out, strings.ToLower(ownerName))
			}
			continue
		}
		if !stopWords[w] {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}
