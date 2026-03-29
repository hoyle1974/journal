package impl

import (
	"context"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/tools"
)

type graphExpandArgs struct {
	NodeID       string `json:"node_id" description:"UUID of the knowledge node to expand" required:"true"`
	Hops         int    `json:"hops" description:"Number of hops to traverse (1 = immediate neighbourhood; 2-3 for multi-hop)" default:"1"`
	LimitPerEdge int    `json:"limit_per_edge" description:"Maximum neighbours per edge type per node (default 10, max 20)" default:"10"`
	Query        string `json:"query" description:"The question or topic you are investigating — required when hops > 1 for semantic pruning of the traversal frontier"`
}

func registerGraphTools() {
	tools.Register(&tools.Tool{
		Name:        "graph_expand",
		Description: "Expand a knowledge graph node by traversing its neighbourhood. hops=1 returns immediate relationship-node edges (node_type=relationship), entity_links, and back-references. hops=2 or hops=3 performs multi-hop BFS with semantic pruning — requires the 'query' field so the traversal follows the semantic scent of your question. Use after semantic_search to explore relationships.",
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

			var queryVec []float32
			if hops > 1 {
				if a.Query == "" {
					return tools.Fail("graph_expand with hops > 1 requires the 'query' field — re-invoke with the question or topic you are investigating so the traversal can prune semantically.")
				}
				vec, err := infra.GenerateEmbedding(ctx, env.GeminiClient(), a.Query, infra.EmbedTaskRetrievalQuery)
				if err != nil {
					return tools.Fail("graph_expand: failed to embed query for pruning: %v", err)
				}
				queryVec = vec
			}

			sg, err := env.MemoryStore().GraphExpand(ctx, a.NodeID, queryVec, hops, limitPerEdge)
			if err != nil {
				return tools.Fail("graph_expand error: %v", err)
			}

			return tools.OK("%s", sg.ToMarkdown(a.NodeID))
		},
	})
}

