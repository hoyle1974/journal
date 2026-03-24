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
func BuildLoomRAGContext(ctx context.Context, app *infra.App, logUUID string, seedNodeIDs []string) (*LoomRAGContext, error) {
	if app == nil {
		return nil, fmt.Errorf("BuildLoomRAGContext: app required")
	}
	ctx, span := infra.StartSpan(ctx, "loom.build_rag_context")
	defer span.End()

	result := &LoomRAGContext{}

	if len(seedNodeIDs) > 0 {
		// Batch fetch all seed nodes (1 RPC for up to 100 seeds).
		seedNodes, err := app.Memory.GetKnowledgeNodesByIDs(ctx, seedNodeIDs)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("loom rag: batch fetch seeds failed", "error", err)
		}

		// Record seed summaries and collect second-hop IDs.
		seenIDs := make(map[string]bool, len(seedNodeIDs)+20)
		for _, id := range seedNodeIDs {
			seenIDs[id] = true
		}
		var hopIDs []string

		for _, node := range seedNodes {
			if node.NodeType == memory.NodeTypeRelationship {
				result.RelationshipSummaries = append(result.RelationshipSummaries,
					fmt.Sprintf("[rel] %s | %s | subj=%s obj=%s",
						node.UUID, node.Content, node.SubjectUUID, node.ObjectUUID))
				for _, hopID := range []string{node.SubjectUUID, node.ObjectUUID} {
					if hopID != "" && !seenIDs[hopID] {
						seenIDs[hopID] = true
						hopIDs = append(hopIDs, hopID)
					}
				}
			} else {
				result.HopNodeSummaries = append(result.HopNodeSummaries,
					fmt.Sprintf("[node] %s | %s", node.UUID, node.Content))
				for _, hopID := range node.EntityLinks {
					if hopID != "" && !seenIDs[hopID] {
						seenIDs[hopID] = true
						hopIDs = append(hopIDs, hopID)
					}
				}
			}
		}

		// Batch fetch second-hop nodes (1 RPC for all hot-edges and subject/objects).
		if len(hopIDs) > 0 {
			hopNodes, err := app.Memory.GetKnowledgeNodesByIDs(ctx, hopIDs)
			if err != nil {
				infra.LoggerFrom(ctx).Warn("loom rag: batch fetch hop nodes failed", "error", err)
			}
			for _, node := range hopNodes {
				result.HopNodeSummaries = append(result.HopNodeSummaries,
					fmt.Sprintf("[hop] %s | %s", node.UUID, node.Content))
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
