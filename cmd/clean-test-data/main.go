// clean-test-data deletes Firestore entries matching a query (e.g. by source).
// Usage: go run ./cmd/clean-test-data -source=old_source
// Optional: -dry-run to only count matching documents.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"google.golang.org/api/iterator"

	"github.com/jackstrohm/jot"
)

func main() {
	source := flag.String("source", "", "Delete entries where source equals this value (required)")
	dryRun := flag.Bool("dry-run", false, "Only count matching documents, do not delete")
	flag.Parse()

	if *source == "" {
		log.Fatal(" -source is required (e.g. -source=old_source)")
	}

	ctx := context.Background()
	app, err := jot.NewApp(ctx)
	if err != nil {
		log.Fatal(err)
	}
	ctx = jot.WithApp(ctx, app)
	client, err := jot.GetFirestoreClient(ctx)
	if err != nil {
		log.Fatal(err)
	}

	iter := client.Collection(jot.EntriesCollection).
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
