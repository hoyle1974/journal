// backfill_links runs the evaluator over journal entries and links extracted facts to knowledge nodes.
// Use after enabling source linkage so existing entries get linked. One-off; safe to run multiple times.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/jackstrohm/jot"
	"github.com/jackstrohm/jot/internal/config"
)

const distanceThreshold = 0.15

func runBackfillLinks() {
	args := os.Args[2:]
	fs := flag.NewFlagSet("backfill-links", flag.ExitOnError)
	limit := fs.Int("limit", 100, "Max journal entries to process (oldest first)")
	dryRun := fs.Bool("dry-run", false, "Only log; do not create or update knowledge nodes")
	_ = fs.Parse(args)

	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	app, err := jot.NewApp(ctx, cfg)
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
				log.Printf("[%d/%d] linked entry %s -> node %s (fact: %q)", i+1, len(entries), e.UUID, existing.UUID, truncateStr(extract.FactToStore, 50))
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
				log.Printf("[%d/%d] created node from entry %s (fact: %q)", i+1, len(entries), e.UUID, truncateStr(extract.FactToStore, 50))
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
			log.Printf("[%d/%d] created node from entry %s (fact: %q)", i+1, len(entries), e.UUID, truncateStr(extract.FactToStore, 50))
		}

		time.Sleep(200 * time.Millisecond)
	}

	log.Printf("Done: linked=%d created=%d skipped=%d errors=%d", linked, created, skipped, errors)
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
