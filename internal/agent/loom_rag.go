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

// BuildLoomRAGContext performs 2-hop context retrieval for a log entry:
//  1. Vector-search top-5 nodes similar to logContent.
//  2. For each result, fetch subject/object nodes and expand their hot_edges (1 more hop).
//  3. Fetch open root tasks (pending + active).
func BuildLoomRAGContext(ctx context.Context, app *infra.App, logContent string) (*LoomRAGContext, error) {
	ctx, span := infra.StartSpan(ctx, "loom.build_rag_context")
	defer span.End()

	result := &LoomRAGContext{}

	// Step 1: Vector search top-5 nodes similar to the log entry.
	queryVec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, logContent, infra.EmbedTaskRetrievalQuery)
	if err != nil {
		return result, fmt.Errorf("loom rag: embedding: %w", err)
	}
	similarNodes, err := app.Memory.QuerySimilarNodes(ctx, queryVec, 5)
	if err != nil {
		return result, fmt.Errorf("loom rag: query similar: %w", err)
	}

	// Step 2: Expand subject/object nodes + their hot_edges.
	seenIDs := make(map[string]bool)
	client, fsErr := app.Firestore(ctx)
	if fsErr != nil {
		return result, fmt.Errorf("loom rag: firestore: %w", fsErr)
	}
	col := client.Collection(memory.KnowledgeCollection)

	for _, rel := range similarNodes {
		result.RelationshipSummaries = append(result.RelationshipSummaries,
			fmt.Sprintf("[rel] %s | %s | subj=%s obj=%s", rel.UUID, rel.Content, rel.SubjectUUID, rel.ObjectUUID))

		for _, nodeID := range []string{rel.SubjectUUID, rel.ObjectUUID} {
			if nodeID == "" || seenIDs[nodeID] {
				continue
			}
			seenIDs[nodeID] = true

			nodeDoc, err := col.Doc(nodeID).Get(ctx)
			if err != nil {
				infra.LoggerFrom(ctx).Warn("loom rag: fetch node failed", "node_id", nodeID, "error", err)
				continue
			}
			nodeData := nodeDoc.Data()
			result.HopNodeSummaries = append(result.HopNodeSummaries,
				fmt.Sprintf("[node] %s | %s", nodeID, getStringFieldFromMap(nodeData, "content")))

			// Expand hot_edges of this node (second hop).
			hotEdges, _ := nodeData["hot_edges"].([]any)
			for _, he := range hotEdges {
				heID, _ := he.(string)
				if heID == "" || seenIDs[heID] {
					continue
				}
				seenIDs[heID] = true
				heDoc, err := col.Doc(heID).Get(ctx)
				if err != nil {
					continue
				}
				result.HopNodeSummaries = append(result.HopNodeSummaries,
					fmt.Sprintf("[hot-edge] %s | %s", heID, getStringFieldFromMap(heDoc.Data(), "content")))
			}
		}
	}

	// Step 3: Fetch open tasks for additional context.
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

// getStringFieldFromMap is a local helper (avoids import cycle with memory package helpers).
func getStringFieldFromMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
