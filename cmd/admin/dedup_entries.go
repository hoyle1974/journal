// dedup_entries finds journal entries with identical (trimmed) content and optionally
// removes all but the first (oldest by timestamp), e.g. repeated "What do I have to do for Gloria's birthday party?"
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/journal"
)

type entryRow struct {
	ID        string
	Content   string
	Timestamp string
}

func runDedupEntries(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("dedup-entries", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", true, "Only list duplicate groups; do not delete (default true)")
	remove := fs.Bool("remove", false, "Remove duplicate entries, keeping the oldest of each content group")
	minDupes := fs.Int("min", 2, "Minimum number of same-content entries to consider a duplicate group")
	_ = fs.Parse(args)

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("Firestore: %v", err)
	}

	entries, err := fetchAllEntries(ctx, client)
	if err != nil {
		log.Fatalf("fetch entries: %v", err)
	}
	log.Printf("Fetched %d journal entries", len(entries))

	// Group by normalized content (trimmed)
	byContent := make(map[string][]entryRow)
	for _, e := range entries {
		key := strings.TrimSpace(e.Content)
		byContent[key] = append(byContent[key], e)
	}

	var toDelete []string
	for content, group := range byContent {
		if len(group) < *minDupes {
			continue
		}
		// Sort by timestamp ascending (oldest first); keep first, delete rest
		sort.Slice(group, func(i, j int) bool {
			return group[i].Timestamp < group[j].Timestamp
		})
		keep := group[0]
		dupes := group[1:]
		for _, d := range dupes {
			toDelete = append(toDelete, d.ID)
		}
		preview := content
		if len(preview) > 70 {
			preview = preview[:70] + "..."
		}
		fmt.Printf("Group (%d entries), keeping oldest [%s] id=%s:\n", len(group), keep.Timestamp, keep.ID)
		for _, d := range dupes {
			fmt.Printf("  [%s] %s (id=%s)\n", d.Timestamp, preview, d.ID)
		}
		fmt.Printf("  -> would remove %d duplicates (content: %q)\n\n", len(dupes), preview)
	}

	if len(toDelete) == 0 {
		fmt.Println("No duplicate groups found (or none meeting -min).")
		return
	}

	fmt.Printf("Total: %d entries would be removed (keeping oldest per content).\n", len(toDelete))

	if !*remove {
		fmt.Println("Run with -remove to delete these entries. Default is -dry-run=true (list only).")
		return
	}

	if *dryRun {
		fmt.Println("Still in dry-run; run with -dry-run=false -remove to apply deletes.")
		return
	}

	// Batch delete (Firestore limit 500 per batch)
	const batchLimit = 500
	for i := 0; i < len(toDelete); i += batchLimit {
		end := i + batchLimit
		if end > len(toDelete) {
			end = len(toDelete)
		}
		batch := toDelete[i:end]
		if err := journal.DeleteEntries(ctx, client, batch); err != nil {
			log.Fatalf("DeleteEntries: %v", err)
		}
		log.Printf("Deleted %d entries (batch %d)", len(batch), i/batchLimit+1)
	}
	fmt.Printf("Removed %d duplicate journal entries.\n", len(toDelete))
}

func fetchAllEntries(ctx context.Context, client *firestore.Client) ([]entryRow, error) {
	iter := client.Collection(journal.EntriesCollection).Documents(ctx)
	defer iter.Stop()

	var out []entryRow
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		data := doc.Data()
		out = append(out, entryRow{
			ID:        doc.Ref.ID,
			Content:   infra.GetStringField(data, "content"),
			Timestamp: infra.GetStringField(data, "timestamp"),
		})
	}
	return out, nil
}
