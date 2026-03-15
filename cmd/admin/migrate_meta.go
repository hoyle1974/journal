// migrate_meta repairs and normalizes metadata on knowledge_nodes documents.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/memory"
)

func runMigrateMeta(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("migrate-meta", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Log updates only, do not write to Firestore")
	_ = fs.Parse(args)

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("Firestore: %v", err)
	}

	updated, err := memory.MigrateKnowledgeMetadata(ctx, client, memory.KnowledgeCollection, *dryRun)
	if err != nil {
		log.Fatalf("MigrateKnowledgeMetadata: %v", err)
	}

	if *dryRun {
		fmt.Printf("Would update %d document(s). Run without -dry-run to apply.\n", updated)
	} else {
		fmt.Printf("Updated %d document(s).\n", updated)
	}
}
