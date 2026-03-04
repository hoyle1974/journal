// backfill-knowledge-links runs the evaluator over journal entries and links extracted facts to knowledge nodes (with journal_entry_ids).
// Use after enabling source linkage so existing entries get linked. One-off; safe to run multiple times (appends entry IDs to existing nodes).
//
// Usage: go run ./cmd/backfill-knowledge-links [-limit=100] [-dry-run]
// -limit: max entries to process (oldest first). Default 100.
// -dry-run: only log what would be done, do not write.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/jackstrohm/jot"
)

const distanceThreshold = 0.15

func main() {
	limit := flag.Int("limit", 100, "Max journal entries to process (oldest first)")
	dryRun := flag.Bool("dry-run", false, "Only log; do not create or update knowledge nodes")
	flag.Parse()

	ctx := context.Background()
	app, err := jot.NewApp(ctx)
	if err != nil {
		log.Fatalf("init app: %v", err)
	}
	ctx = jot.WithApp(ctx, app)

	entries, err := jot.GetEntriesAsc(ctx, *limit)
	if err != nil {
		log.Fatalf("get entries: %v", err)
	}
	log.Printf("Processing %d entries (dry-run=%v)", len(entries), *dryRun)

	linked, created, skipped, errors := 0, 0, 0, 0
	for i, e := range entries {
		extract, err := jot.RunEvaluatorExtract(ctx, e.Content)
		if err != nil {
			log.Printf("[%d/%d] %s extract error: %v", i+1, len(entries), e.UUID, err)
			errors++
			continue
		}
		if extract == nil || extract.FactToStore == "" || extract.Significance < 0.5 {
			skipped++
			continue
		}

		vec, err := jot.GenerateEmbedding(ctx, extract.FactToStore, jot.EmbedTaskRetrievalDocument)
		if err != nil {
			log.Printf("[%d/%d] %s embedding error: %v", i+1, len(entries), e.UUID, err)
			errors++
			continue
		}

		existing, err := jot.FindNearestWithThreshold(ctx, vec, distanceThreshold)
		if err != nil {
			log.Printf("[%d/%d] %s find-nearest error: %v", i+1, len(entries), e.UUID, err)
			errors++
			continue
		}

		if existing != nil {
			action, collErr := jot.EvaluateFactCollision(ctx, extract.FactToStore, existing.Content)
			if collErr != nil {
				action = "insert"
			}
			if action == "update" {
				if !*dryRun {
					if err := jot.AppendJournalEntryIDsToNode(ctx, existing.UUID, []string{e.UUID}); err != nil {
						log.Printf("[%d/%d] %s append link error: %v", i+1, len(entries), e.UUID, err)
						errors++
						continue
					}
				}
				linked++
				log.Printf("[%d/%d] linked entry %s -> node %s (fact: %q)", i+1, len(entries), e.UUID, existing.UUID, truncate(extract.FactToStore, 50))
			} else {
				// insert new node
				if !*dryRun {
					nodeType := "fact"
					if extract.Domain == "relationship" {
						nodeType = "person"
					} else if extract.Domain == "work" {
						nodeType = "project"
					}
					if _, err := jot.UpsertSemanticMemory(ctx, extract.FactToStore, nodeType, extract.Domain, extract.Significance, nil, []string{e.UUID}); err != nil {
						log.Printf("[%d/%d] %s upsert error: %v", i+1, len(entries), e.UUID, err)
						errors++
						continue
					}
				}
				created++
				log.Printf("[%d/%d] created node from entry %s (fact: %q)", i+1, len(entries), e.UUID, truncate(extract.FactToStore, 50))
			}
		} else {
			if !*dryRun {
				nodeType := "fact"
				if extract.Domain == "relationship" {
					nodeType = "person"
				} else if extract.Domain == "work" {
					nodeType = "project"
				}
				if _, err := jot.UpsertSemanticMemory(ctx, extract.FactToStore, nodeType, extract.Domain, extract.Significance, nil, []string{e.UUID}); err != nil {
					log.Printf("[%d/%d] %s upsert error: %v", i+1, len(entries), e.UUID, err)
					errors++
					continue
				}
			}
			created++
			log.Printf("[%d/%d] created node from entry %s (fact: %q)", i+1, len(entries), e.UUID, truncate(extract.FactToStore, 50))
		}

		// Rate-limit Gemini/Vertex
		time.Sleep(200 * time.Millisecond)
	}

	log.Printf("Done: linked=%d created=%d skipped=%d errors=%d", linked, created, skipped, errors)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
