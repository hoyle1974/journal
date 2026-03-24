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

// BuildLoomRAGContext performs 2-hop context retrieval from seed node IDs produced by the refinery:
//  1. For each seed node, determine if it's a relationship or entity node.
//  2. Relationship seeds: record summary, then follow subject+object as second hop.
//  3. Entity seeds: record summary, then expand hot_edges as second hop.
//  4. Always include open root tasks for task-aware reasoning.
func BuildLoomRAGContext(ctx context.Context, app *infra.App, logUUID string, seedNodeIDs []string) (*LoomRAGContext, error) {
	if app == nil {
		return nil, fmt.Errorf("BuildLoomRAGContext: app required")
	}
	ctx, span := infra.StartSpan(ctx, "loom.build_rag_context")
	defer span.End()

	result := &LoomRAGContext{}

	if len(seedNodeIDs) > 0 {
		client, err := app.Firestore(ctx)
		if err != nil {
			return result, fmt.Errorf("loom rag: firestore: %w", err)
		}
		col := client.Collection(memory.KnowledgeCollection)
		seenIDs := make(map[string]bool)

		for _, nodeID := range seedNodeIDs {
			if nodeID == "" || seenIDs[nodeID] {
				continue
			}
			seenIDs[nodeID] = true

			doc, err := col.Doc(nodeID).Get(ctx)
			if err != nil {
				infra.LoggerFrom(ctx).Warn("loom rag: fetch seed node failed", "node_id", nodeID, "error", err)
				continue
			}
			data := doc.Data()
			nodeType, _ := data["node_type"].(string)
			content := getStringFieldFromMap(data, "content")

			if nodeType == memory.NodeTypeRelationship {
				// Relationship seed: record summary, then follow subject+object as second hop.
				subj, _ := data["subject_uuid"].(string)
				obj, _ := data["object_uuid"].(string)
				result.RelationshipSummaries = append(result.RelationshipSummaries,
					fmt.Sprintf("[rel] %s | %s | subj=%s obj=%s", nodeID, content, subj, obj))
				for _, hopID := range []string{subj, obj} {
					if hopID == "" || seenIDs[hopID] {
						continue
					}
					seenIDs[hopID] = true
					hopDoc, err := col.Doc(hopID).Get(ctx)
					if err != nil {
						continue
					}
					hopData := hopDoc.Data()
					result.HopNodeSummaries = append(result.HopNodeSummaries,
						fmt.Sprintf("[node] %s | %s", hopID, getStringFieldFromMap(hopData, "content")))
					hotEdges, _ := hopData["hot_edges"].([]any)
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
			} else {
				// Entity seed: record summary, then expand hot_edges as second hop.
				result.HopNodeSummaries = append(result.HopNodeSummaries,
					fmt.Sprintf("[node] %s | %s", nodeID, content))
				hotEdges, _ := data["hot_edges"].([]any)
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

// getStringFieldFromMap is a local helper (avoids import cycle with memory package helpers).
func getStringFieldFromMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
