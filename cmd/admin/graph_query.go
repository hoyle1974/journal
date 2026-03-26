// graph_query searches the knowledge graph by keyword or natural-language string
// and prints the subgraph up to a given hop depth.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
)

func runGraphQuery(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("graph-query", flag.ExitOnError)
	depth := fs.Int("depth", 1, "Graph traversal depth (hops); 1 = immediate neighbours, 2-3 for multi-hop")
	limit := fs.Int("limit", 10, "Max seed nodes returned by the initial vector search")
	limitPerEdge := fs.Int("limit-per-edge", 5, "Max neighbours per edge type per node during expansion")
	minMatch := fs.Float64("min-match", 0.52, "Minimum cosine similarity score [0,1] required for a seed node to be included")
	dotFile := fs.String("dot-file", "", "If set, write a Graphviz DOT file to this path")
	_ = fs.Parse(args)

	query := strings.Join(fs.Args(), " ")
	if query == "" {
		log.Fatal("Usage: graph-query [-depth=1] [-limit=10] [-limit-per-edge=5] <keyword or phrase>")
	}

	cfg := app.Config()
	infra.InitObservability(cfg)

	// 1. Embed the query — used for both seed discovery and multi-hop semantic pruning.
	queryVec, err := infra.GenerateEmbedding(ctx, cfg.GoogleCloudProject, query, infra.EmbedTaskRetrievalQuery)
	if err != nil {
		log.Fatalf("embed query: %v", err)
	}

	// 2. Vector search to find seed nodes.
	seeds, err := app.MemoryGraph().QuerySimilar(ctx, queryVec, memory.SearchOptions{Limit: *limit, MinSignificance: 0.5})
	if err != nil {
		log.Fatalf("vector search: %v", err)
	}
	// Filter seeds below the minimum match threshold.
	filtered := seeds[:0]
	for _, s := range seeds {
		if s.QueryScore >= *minMatch {
			filtered = append(filtered, s)
		}
	}
	seeds = filtered

	if len(seeds) == 0 {
		fmt.Printf("No matching nodes found (min-match=%.2f; try lowering with -min-match=0.4).\n", *minMatch)
		return
	}

	fmt.Printf("Query: %q  |  seeds: %d  |  depth: %d  |  min-match: %.2f\n\n", query, len(seeds), *depth, *minMatch)
	fmt.Println("## Seed Matches")
	for _, s := range seeds {
		nt := s.NodeType
		if len(nt) > 0 {
			nt = strings.ToUpper(nt[:1]) + nt[1:]
		}
		fmt.Printf("* %s[%s]  match:%.2f  sig:%.1f\n", nt, s.Content, s.QueryScore, s.SignificanceWeight)
	}
	fmt.Println()

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

	if *dotFile != "" {
		if err := os.WriteFile(*dotFile, []byte(sg.ToDOT()), 0o644); err != nil {
			log.Fatalf("write dot file: %v", err)
		}
	}
}
