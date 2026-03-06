// dedup finds duplicate knowledge nodes (by exact content or cosine similarity) and merges them into a canonical node per cluster.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"strings"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/memory"
)

const batchLimit = 400

// nodeRow holds fields we need from a knowledge_nodes document (embedding extracted manually).
type nodeRow struct {
	UUID            string
	Content         string
	Timestamp       string
	JournalEntryIDs []string
	Embedding       []float32
}

func runDedup(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("dedup", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Log merge plan only; do not write to Firestore")
	threshold := fs.Float64("threshold", 0.95, "Cosine similarity threshold (>=) to consider nodes duplicates")
	_ = fs.Parse(args)

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("Firestore: %v", err)
	}

	nodes, err := fetchAllKnowledgeNodes(ctx, client)
	if err != nil {
		log.Fatalf("fetch nodes: %v", err)
	}
	log.Printf("Fetched %d knowledge nodes (dry-run=%v, threshold=%.2f)", len(nodes), *dryRun, *threshold)

	clusters := clusterNodes(nodes, *threshold)
	clusterCount := 0
	for _, c := range clusters {
		if len(c) > 1 {
			clusterCount++
		}
	}
	log.Printf("Found %d clusters with more than one node", clusterCount)

	batch := client.Batch()
	ops := 0
	for _, cluster := range clusters {
		if len(cluster) <= 1 {
			continue
		}
		canonical, others := pickCanonical(cluster)
		allEntryIDs := mergeJournalEntryIDs(cluster)
		preview := truncatePreview(canonical.Content, 60)

		if *dryRun {
			log.Printf("Would merge %d nodes into canonical node %s: %s", len(cluster), canonical.UUID, preview)
			continue
		}

		// Merge all journal_entry_ids into the canonical node
		if ops+1 > batchLimit {
			if _, err := batch.Commit(ctx); err != nil {
				log.Fatalf("batch commit: %v", err)
			}
			batch = client.Batch()
			ops = 0
		}
		ref := client.Collection(memory.KnowledgeCollection).Doc(canonical.UUID)
		batch.Update(ref, []firestore.Update{{Path: "journal_entry_ids", Value: allEntryIDs}})
		ops++

		for _, n := range others {
			if ops+1 > batchLimit {
				if _, err := batch.Commit(ctx); err != nil {
					log.Fatalf("batch commit: %v", err)
				}
				batch = client.Batch()
				ops = 0
			}
			ref := client.Collection(memory.KnowledgeCollection).Doc(n.UUID)
			batch.Delete(ref)
			ops++
		}

		log.Printf("Merged %d nodes into canonical node %s: %s", len(cluster), canonical.UUID, preview)
	}

	if !*dryRun && ops > 0 {
		if _, err := batch.Commit(ctx); err != nil {
			log.Fatalf("batch commit: %v", err)
		}
	}
}

func fetchAllKnowledgeNodes(ctx context.Context, client *firestore.Client) ([]nodeRow, error) {
	iter := client.Collection(memory.KnowledgeCollection).Documents(ctx)
	defer iter.Stop()

	var nodes []nodeRow
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		data := doc.Data()
		emb, _ := data["embedding"].(firestore.Vector32)
		embedding := []float32(emb)
		if len(embedding) == 0 {
			if raw, ok := data["embedding"].([]interface{}); ok {
				embedding = make([]float32, 0, len(raw))
				for _, v := range raw {
					if f, ok := v.(float64); ok {
						embedding = append(embedding, float32(f))
					}
				}
			}
		}
		nodes = append(nodes, nodeRow{
			UUID:            doc.Ref.ID,
			Content:         infra.GetStringField(data, "content"),
			Timestamp:       infra.GetStringField(data, "timestamp"),
			JournalEntryIDs: infra.GetStringSliceField(data, "journal_entry_ids"),
			Embedding:       embedding,
		})
	}
	return nodes, nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// clusterNodes groups nodes by exact content match or cosine similarity >= threshold (union-find).
func clusterNodes(nodes []nodeRow, threshold float64) [][]nodeRow {
	parent := make([]int, len(nodes))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	union := func(i, j int) {
		pi, pj := find(i), find(j)
		if pi != pj {
			parent[pi] = pj
		}
	}

	for i := range nodes {
		for j := i + 1; j < len(nodes); j++ {
			if nodes[i].Content == nodes[j].Content {
				union(i, j)
				continue
			}
			if len(nodes[i].Embedding) > 0 && len(nodes[j].Embedding) > 0 &&
				len(nodes[i].Embedding) == len(nodes[j].Embedding) &&
				cosineSimilarity(nodes[i].Embedding, nodes[j].Embedding) >= threshold {
				union(i, j)
			}
		}
	}

	rootToIdx := make(map[int][]nodeRow)
	for i := range nodes {
		r := find(i)
		rootToIdx[r] = append(rootToIdx[r], nodes[i])
	}
	out := make([][]nodeRow, 0, len(rootToIdx))
	for _, group := range rootToIdx {
		out = append(out, group)
	}
	return out
}

func pickCanonical(cluster []nodeRow) (canonical nodeRow, others []nodeRow) {
	oldest := 0
	for i := 1; i < len(cluster); i++ {
		if tsLess(cluster[i].Timestamp, cluster[oldest].Timestamp) {
			oldest = i
		}
	}
	canonical = cluster[oldest]
	others = make([]nodeRow, 0, len(cluster)-1)
	for i, n := range cluster {
		if i != oldest {
			others = append(others, n)
		}
	}
	return canonical, others
}

// tsLess returns true if a is older than b (a should be canonical over b). Empty timestamp counts as newest.
func tsLess(a, b string) bool {
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	return a < b
}

func mergeJournalEntryIDs(cluster []nodeRow) []string {
	seen := make(map[string]bool)
	var out []string
	for _, n := range cluster {
		for _, id := range n.JournalEntryIDs {
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

func truncatePreview(content string, maxLen int) string {
	s := strings.TrimSpace(content)
	if len(s) == 0 {
		return "(no content)"
	}
	if len([]rune(s)) <= maxLen {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%q...", string([]rune(s)[:maxLen]))
}
