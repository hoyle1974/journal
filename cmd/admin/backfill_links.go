// backfill_links is deprecated.
// Legacy evaluator-driven fact extraction was removed; use refinery-based ingest for new entries.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/jackstrohm/jot/internal/infra"
)

func runBackfillLinks(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("backfill-links", flag.ExitOnError)
	_ = fs.Int("limit", 100, "Deprecated")
	_ = fs.Bool("dry-run", false, "Deprecated")
	_ = fs.Parse(args)

	_ = ctx
	_ = app
	log.Printf("backfill-links is deprecated: evaluator-based fact extraction has been removed in favor of synchronous refinery ingest")
}
