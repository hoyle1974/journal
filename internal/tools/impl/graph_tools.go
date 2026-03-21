package impl

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/tools"
)

type graphExpandArgs struct {
	NodeID       string `json:"node_id" description:"UUID of the knowledge node to expand (use the UUID from a previous semantic_search or list_knowledge result)" required:"true"`
	Hops         int    `json:"hops" description:"Number of hops to traverse (currently only 1 is supported; default 1)" default:"1"`
	LimitPerEdge int    `json:"limit_per_edge" description:"Maximum number of neighbours to return per edge type (default 10, max 20)" default:"10"`
}

func registerGraphTools() {
	tools.Register(&tools.Tool{
		Name:        "graph_expand",
		Description: "Expand a knowledge graph node by fetching its 1-hop neighbourhood: outgoing SPO edges (subject→object), incoming entity_link edges (nodes that reference this node), and directly linked nodes from entity_links. Use this after semantic_search to explore relationships around a discovered node UUID.",
		Category:    "knowledge",
		Args:        &graphExpandArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*graphExpandArgs)
			if a.NodeID == "" {
				return tools.MissingParam("node_id")
			}
			hops := a.Hops
			if hops <= 0 {
				hops = 1
			}
			limitPerEdge := clampInt(a.LimitPerEdge, 10, 1, 20)

			result, err := env.MemoryStore().GraphExpand(ctx, a.NodeID, hops, limitPerEdge)
			if err != nil {
				return tools.Fail("graph_expand error: %v", err)
			}

			return tools.OK("%s", formatGraphExpandResult(result))
		},
	})
}

// formatGraphExpandResult renders a GraphExpandResult as a human/LLM-readable string.
func formatGraphExpandResult(r *memory.GraphExpandResult) string {
	var sb strings.Builder

	// Seed node
	seed := r.Seed
	sb.WriteString(fmt.Sprintf("Seed node [%s]: %s\n", seed.NodeType, seed.Content))
	sb.WriteString(fmt.Sprintf("   UUID: %s\n", seed.UUID))
	if seed.Timestamp != "" {
		sb.WriteString(fmt.Sprintf("   Timestamp: %s\n", seed.Timestamp))
	}
	if seed.Metadata != "" && seed.Metadata != "{}" {
		sb.WriteString(fmt.Sprintf("   Metadata: %s\n", seed.Metadata))
	}

	writeSection := func(header string, nodes []memory.KnowledgeNode) {
		sb.WriteString(fmt.Sprintf("\n%s (%d):\n", header, len(nodes)))
		if len(nodes) == 0 {
			sb.WriteString("  (none)\n")
			return
		}
		for i, n := range nodes {
			content := n.Content
			if len(content) > 200 {
				content = content[:197] + "..."
			}
			sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, n.NodeType, content))
			if n.UUID != "" {
				sb.WriteString(fmt.Sprintf("     UUID: %s\n", n.UUID))
			}
			if n.Predicate != "" {
				sb.WriteString(fmt.Sprintf("     Predicate: %s\n", n.Predicate))
			}
			if n.ObjectUUID != "" {
				sb.WriteString(fmt.Sprintf("     ObjectUUID: %s\n", n.ObjectUUID))
			}
		}
	}

	writeSection("Outgoing edges (subject→object SPO triples)", r.Outgoing)
	writeSection("Incoming edges (nodes referencing this node)", r.Incoming)
	writeSection("Directly linked nodes (entity_links)", r.Linked)

	return strings.TrimRight(sb.String(), "\n")
}
