// migrate_knowledge_metadata repairs and normalizes metadata on knowledge_nodes documents.
// Usage: go run ./cmd/migrate_knowledge_metadata [-dry-run]
// Use -dry-run to log what would be updated without writing. Without -dry-run, applies changes.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/jackstrohm/jot"
	"github.com/jackstrohm/jot/internal/memory"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "Log updates only, do not write to Firestore")
	flag.Parse()

	ctx := context.Background()
	app, err := jot.NewApp(ctx)
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
