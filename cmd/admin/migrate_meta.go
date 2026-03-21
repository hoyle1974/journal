// migrate_meta repairs and normalizes metadata on knowledge_nodes documents.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/jackstrohm/jot/internal/infra"
)

func runMigrateMeta(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("migrate-meta", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Log updates only, do not write to Firestore")
	_ = fs.Parse(args)

	updated, err := app.Memory.MigrateKnowledgeMetadata(ctx, *dryRun)
	if err != nil {
		log.Fatalf("MigrateKnowledgeMetadata: %v", err)
	}

	if *dryRun {
		fmt.Printf("Would update %d document(s). Run without -dry-run to apply.\n", updated)
	} else {
		fmt.Printf("Updated %d document(s).\n", updated)
	}
}
