// backfill_links runs the evaluator over journal entries and links extracted facts to knowledge nodes.
// Use after enabling source linkage so existing entries get linked. One-off; safe to run multiple times.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
)

const distanceThreshold = 0.15

func runBackfillLinks(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("backfill-links", flag.ExitOnError)
	limit := fs.Int("limit", 100, "Max journal entries to process (oldest first)")
	dryRun := fs.Bool("dry-run", false, "Only log; do not create or update knowledge nodes")
	_ = fs.Parse(args)

	cfg := app.Config()
	entries, err := journal.GetEntriesAsc(ctx, *limit)
	if err != nil {
		log.Fatalf("get entries: %v", err)
	}
	log.Printf("Processing %d entries (dry-run=%v)", len(entries), *dryRun)

	linked, created, skipped, errors := 0, 0, 0, 0
	for i, e := range entries {
		extract, err := agent.RunEvaluatorExtract(ctx, e.Content)
		if err != nil {
			log.Printf("[%d/%d] %s extract error: %v", i+1, len(entries), e.UUID, err)
			errors++
			continue
		}
		if extract == nil || extract.FactToStore == "" || extract.Significance < 0.5 {
			skipped++
			continue
		}

		vec, err := infra.GenerateEmbedding(ctx, cfg.GoogleCloudProject, extract.FactToStore, infra.EmbedTaskRetrievalDocument)
		if err != nil {
			log.Printf("[%d/%d] %s embedding error: %v", i+1, len(entries), e.UUID, err)
			errors++
			continue
		}

		existing, err := memory.FindNearestWithThreshold(ctx, vec, distanceThreshold)
		if err != nil {
			log.Printf("[%d/%d] %s find-nearest error: %v", i+1, len(entries), e.UUID, err)
			errors++
			continue
		}

		if existing != nil {
			action, collErr := infra.EvaluateFactCollision(ctx, cfg, extract.FactToStore, existing.Content)
			if collErr != nil {
				action = "insert"
			}
			if action == "update" {
				if !*dryRun {
					if err := memory.AppendJournalEntryIDsToNode(ctx, existing.UUID, []string{e.UUID}); err != nil {
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
					if _, err := memory.UpsertSemanticMemory(ctx, extract.FactToStore, nodeType, extract.Domain, extract.Significance, nil, []string{e.UUID}); err != nil {
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
				if _, err := memory.UpsertSemanticMemory(ctx, extract.FactToStore, nodeType, extract.Domain, extract.Significance, nil, []string{e.UUID}); err != nil {
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
