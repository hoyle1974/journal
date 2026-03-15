// clean deletes Firestore entries matching a query (e.g. by source). Subcommand: clean-test.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"google.golang.org/api/iterator"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/journal"
)

func runCleanTest(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("clean-test", flag.ExitOnError)
	source := fs.String("source", "", "Delete entries where source equals this value (required)")
	dryRun := fs.Bool("dry-run", false, "Only count matching documents, do not delete")
	_ = fs.Parse(args)

	if *source == "" {
		log.Fatal(" -source is required (e.g. -source=old_source)")
	}

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("Firestore: %v", err)
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
