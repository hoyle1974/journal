// strip_done scans all journal entries and removes a trailing "done." or "done" from content, then rewrites the entry.
// One-off migration so stored content no longer includes the Google Doc sync trigger.
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

func runStripDone(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("strip-done", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Only list entries that would be changed; do not update")
	_ = fs.Parse(args)

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("Firestore: %v", err)
	}

	iter := client.Collection(journal.EntriesCollection).Documents(ctx)
	defer iter.Stop()

	updated := 0
	batch := client.Batch()
	batchCount := 0
	const batchLimit = 500

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
		newContent := stripTrailingDone(content)
		if newContent == content {
			continue
		}

		updated++
		if *dryRun {
			preview := content
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
			fmt.Printf("%s: would strip trailing done (content preview: %q)\n", doc.Ref.ID, preview)
			continue
		}

		batch.Update(doc.Ref, []firestore.Update{{Path: "content", Value: newContent}})
		batchCount++
		if batchCount >= batchLimit {
			if _, err := batch.Commit(ctx); err != nil {
				log.Fatalf("batch commit: %v", err)
			}
			batch = client.Batch()
			batchCount = 0
		}
	}

	if !*dryRun && batchCount > 0 {
		if _, err := batch.Commit(ctx); err != nil {
			log.Fatalf("batch commit: %v", err)
		}
	}

	if *dryRun {
		fmt.Printf("Would update %d entries. Run without -dry-run to apply.\n", updated)
	} else {
		fmt.Printf("Updated %d entries (removed trailing 'done.' or 'done' from content).\n", updated)
	}
}

// stripTrailingDone returns content with a trailing "done." or "done" (case-insensitive) removed.
// The trigger text and any preceding trailing whitespace/newlines are stripped.
func stripTrailingDone(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return content
	}
	lower := strings.ToLower(trimmed)
	if lower == "done." || lower == "done" {
		return ""
	}
	// Remove trailing "done." or "done" and any whitespace before it
	if strings.HasSuffix(lower, "done.") {
		trimmed = trimmed[:len(trimmed)-5]
	} else if strings.HasSuffix(lower, "done") {
		trimmed = trimmed[:len(trimmed)-4]
	} else {
		return content
	}
	return strings.TrimSpace(trimmed)
}
