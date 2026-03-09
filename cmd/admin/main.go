// admin runs administrative subcommands: backfill-links, clean-test, dedup, dedup-entries, migrate-meta, show-logged, show-processed, strip-done.
// Usage: go run ./cmd/admin <subcommand> [flags]
//   backfill-links   - link journal entries to knowledge nodes ([-limit=100] [-dry-run])
//   clean-test       - delete entries by source (-source=required [-dry-run])
//   dedup            - merge duplicate knowledge nodes ([-dry-run] [-threshold=0.95])
//   dedup-entries    - find/remove duplicate journal entries (same content); keep oldest ([-min=2] [-dry-run] [-remove])
//   migrate-meta     - repair knowledge_nodes metadata ([-dry-run])
//   show-logged      - list journal entries whose content contains "Logged" ([-preview=120] [-remove] [-dry-run])
//   show-processed   - list journal entries whose content contains "Processed" ([-preview=120] [-remove] [-dry-run])
//   strip-done       - remove trailing "done." / "done" from journal entry content ([-dry-run])
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/pkg/infra"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <backfill-links|clean-test|dedup|dedup-entries|migrate-meta|show-logged|show-processed|strip-done> [flags]\n", os.Args[0])
		os.Exit(1)
	}
	subcommand := os.Args[1]
	args := os.Args[2:]

	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	app, err := infra.NewApp(ctx, cfg, nil, nil)
	if err != nil {
		log.Fatalf("NewApp: %v", err)
	}
	ctx = infra.WithApp(ctx, app)

	switch subcommand {
	case "backfill-links":
		runBackfillLinks(ctx, app, args)
	case "clean-test":
		runCleanTest(ctx, app, args)
	case "dedup":
		runDedup(ctx, app, args)
	case "dedup-entries":
		runDedupEntries(ctx, app, args)
	case "migrate-meta":
		runMigrateMeta(ctx, app, args)
	case "show-logged":
		runShowLogged(ctx, app, args)
	case "show-processed":
		runShowProcessed(ctx, app, args)
	case "strip-done":
		runStripDone(ctx, app, args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand %q\n", subcommand)
		os.Exit(1)
	}
}
