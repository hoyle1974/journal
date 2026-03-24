// graph_query searches the knowledge graph by keyword or natural-language string
// and prints the subgraph up to a given hop depth.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
)

func runGraphQuery(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("graph-query", flag.ExitOnError)
	depth := fs.Int("depth", 1, "Graph traversal depth (hops); 1 = immediate neighbours, 2-3 for multi-hop")
	limit := fs.Int("limit", 10, "Max seed nodes returned by the initial keyword/semantic search")
	limitPerEdge := fs.Int("limit-per-edge", 5, "Max neighbours per edge type per node during expansion")
	_ = fs.Parse(args)

	query := strings.Join(fs.Args(), " ")
	if query == "" {
		log.Fatal("Usage: graph-query [-depth=1] [-limit=10] [-limit-per-edge=5] <keyword or phrase>")
	}

	cfg := app.Config()
	infra.InitObservability(cfg)

	// 1. Keyword search to find seed nodes.
	seeds, err := app.MemoryGraph().SearchKeywords(ctx, query, *limit)
	if err != nil {
		log.Fatalf("keyword search: %v", err)
	}
	if len(seeds) == 0 {
		fmt.Println("No matching nodes found.")
		return
	}

	// 2. Embed the query once for semantic pruning during multi-hop traversal.
	var queryVec []float32
	if *depth > 1 {
		queryVec, err = infra.GenerateEmbedding(ctx, cfg.GoogleCloudProject, query, infra.EmbedTaskRetrievalQuery)
		if err != nil {
			log.Fatalf("embed query: %v", err)
		}
	}

	fmt.Printf("Query: %q  |  seeds: %d  |  depth: %d\n\n", query, len(seeds), *depth)

	// 3. Collect seed IDs (deduplicated) and expand into one normalized graph.
	seedIDs := make([]string, 0, len(seeds))
	seen := make(map[string]bool, len(seeds))
	for _, node := range seeds {
		if !seen[node.UUID] {
			seen[node.UUID] = true
			seedIDs = append(seedIDs, node.UUID)
		}
	}

	sg, err := app.MemoryStore().ExpandMulti(ctx, seedIDs, queryVec, *depth, *limitPerEdge)
	if err != nil {
		log.Fatalf("graph_expand: %v", err)
	}
	fmt.Println(sg.ToMarkdownFull())
}
