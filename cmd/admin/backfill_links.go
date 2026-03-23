// backfill_links runs the evaluator over journal entries and links extracted facts to knowledge nodes.
// Use after enabling source linkage so existing entries get linked. One-off; safe to run multiple times.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/jackstrohm/jot/internal/infra"
)

func runBackfillLinks(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("backfill-links", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Only log; do not create or update knowledge nodes")
	_ = fs.Parse(args)
	_ = ctx
	_ = app
	log.Printf("backfill-links is deprecated: Refinery now performs synchronous graph extraction at ingest time (dry-run=%v)", *dryRun)
}
