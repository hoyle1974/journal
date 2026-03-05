// migrate_meta repairs and normalizes metadata on knowledge_nodes documents.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jackstrohm/jot"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/memory"
)

func runMigrateMeta() {
	args := os.Args[2:]
	fs := flag.NewFlagSet("migrate-meta", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Log updates only, do not write to Firestore")
	_ = fs.Parse(args)

	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	app, err := jot.NewApp(ctx, cfg)
	if err != nil {
		log.Fatalf("NewApp: %v", err)
	}
	ctx = jot.WithApp(ctx, app)

	client, err := jot.GetFirestoreClient(ctx)
	if err != nil {
		log.Fatalf("GetFirestoreClient: %v", err)
	}

	updated, err := memory.MigrateKnowledgeMetadata(ctx, client, jot.KnowledgeCollection, *dryRun)
	if err != nil {
		log.Fatalf("MigrateKnowledgeMetadata: %v", err)
	}

	if *dryRun {
		fmt.Printf("Would update %d document(s). Run without -dry-run to apply.\n", updated)
	} else {
		fmt.Printf("Updated %d document(s).\n", updated)
	}
}
