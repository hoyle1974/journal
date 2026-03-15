// show_processed scans all journal entries and prints those whose content contains "Processed".
// With -remove, deletes those entries (use -dry-run to preview).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/journal"
)

func runShowProcessed(ctx context.Context, app *infra.App, args []string) {
	const batchLimit = 500
	fs := flag.NewFlagSet("show-processed", flag.ExitOnError)
	previewLen := fs.Int("preview", 120, "Max characters of content to show per entry")
	remove := fs.Bool("remove", false, "Delete entries containing \"Processed\"")
	dryRun := fs.Bool("dry-run", false, "With -remove: only list what would be deleted; do not delete")
	_ = fs.Parse(args)

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("Firestore: %v", err)
	}

	iter := client.Collection(journal.EntriesCollection).Documents(ctx)
	defer iter.Stop()

	var toDelete []*firestore.DocumentRef
	count := 0
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		data := doc.Data()
		content, _ := data["content"].(string)
		if !strings.Contains(content, "Processed") {
			continue
		}

		count++
		if *remove {
			toDelete = append(toDelete, doc.Ref)
		}
		preview := content
		if *previewLen > 0 && len(preview) > *previewLen {
			preview = preview[:*previewLen] + "..."
		}
		source, _ := data["source"].(string)
		if *remove && *dryRun {
			fmt.Printf("Would delete %s (source=%s): %q\n", doc.Ref.ID, source, preview)
		} else if !*remove {
			fmt.Printf("--- %s (source=%s) ---\n%s\n\n", doc.Ref.ID, source, preview)
		}
	}

	if *remove {
		if *dryRun {
			fmt.Printf("Would delete %d entries. Run with -remove without -dry-run to apply.\n", count)
			return
		}
		for i := 0; i < len(toDelete); i += batchLimit {
			end := i + batchLimit
			if end > len(toDelete) {
				end = len(toDelete)
			}
			batch := client.Batch()
			for _, ref := range toDelete[i:end] {
				batch.Delete(ref)
			}
			if _, err := batch.Commit(ctx); err != nil {
				log.Fatalf("batch delete: %v", err)
			}
		}
		fmt.Printf("Deleted %d entries containing \"Processed\".\n", count)
	} else {
		fmt.Printf("Found %d entries containing \"Processed\".\n", count)
	}
}
