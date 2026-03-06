// clean deletes Firestore entries matching a query (e.g. by source). Subcommand: clean-test.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"google.golang.org/api/iterator"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
)

func runCleanTest() {
	args := os.Args[2:]
	fs := flag.NewFlagSet("clean-test", flag.ExitOnError)
	source := fs.String("source", "", "Delete entries where source equals this value (required)")
	dryRun := fs.Bool("dry-run", false, "Only count matching documents, do not delete")
	_ = fs.Parse(args)

	if *source == "" {
		log.Fatal(" -source is required (e.g. -source=old_source)")
	}

	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	app, err := infra.NewApp(ctx, cfg, nil, nil)
	if err != nil {
		log.Fatal(err)
	}
	ctx = infra.WithApp(ctx, app)
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		log.Fatal(err)
	}

	iter := client.Collection(journal.EntriesCollection).
		Where("source", "==", *source).
		Documents(ctx)
	defer iter.Stop()

	batch := client.Batch()
	count := 0

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		if !*dryRun {
			batch.Delete(doc.Ref)
		}
		count++

		if !*dryRun && count%500 == 0 {
			_, err := batch.Commit(ctx)
			if err != nil {
				log.Fatal(err)
			}
			batch = client.Batch()
		}
	}

	if !*dryRun && count%500 != 0 {
		_, err := batch.Commit(ctx)
		if err != nil {
			log.Fatal(err)
		}
	}

	if *dryRun {
		fmt.Printf("Would delete %d entries (source=%q). Run without -dry-run to delete.\n", count, *source)
	} else {
		fmt.Printf("Deleted %d entries (source=%q).\n", count, *source)
	}
}
