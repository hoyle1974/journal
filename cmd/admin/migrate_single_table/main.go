// migrate_single_table copies existing "entries" and "knowledge_nodes" documents into the unified "journal" collection.
// It is idempotent: documents that already exist in "journal" (by UUID) are skipped.
//
// Usage:
//
//	go run ./cmd/admin/migrate_single_table [-dry-run] [-limit=1000]
//
// Flags:
//
//	-dry-run   Print what would be copied without writing to Firestore.
//	-limit     Maximum number of documents to process per source collection (default 10000).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/infra"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "Print what would be copied without writing")
	limit := flag.Int("limit", 10000, "Max documents to migrate per source collection")
	flag.Parse()

	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	app, err := infra.NewApp(ctx, cfg, nil, nil)
	if err != nil {
		log.Fatalf("infra.NewApp: %v", err)
	}
	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("firestore: %v", err)
	}

	entriesCopied, err := migrateCollection(ctx, client, "entries", "log", *limit, *dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate entries: %v\n", err)
	}
	knowledgeCopied, err := migrateCollection(ctx, client, "knowledge_nodes", "", *limit, *dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate knowledge_nodes: %v\n", err)
	}

	mode := "migrated"
	if *dryRun {
		mode = "would migrate"
	}
	fmt.Printf("Done: %s %d entries and %d knowledge nodes into 'journal'.\n", mode, entriesCopied, knowledgeCopied)
}

// migrateCollection copies all documents from srcCollection into "journal".
// If forceNodeType is non-empty, it is written as the node_type (used to tag entries as "log").
// Documents whose UUID already exists in "journal" are skipped (idempotent).
func migrateCollection(ctx context.Context, client *firestore.Client, srcCollection, forceNodeType string, limit int, dryRun bool) (int, error) {
	const destCollection = "journal"
	iter := client.Collection(srcCollection).Limit(limit).Documents(ctx)
	defer iter.Stop()

	copied := 0
	skipped := 0
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return copied, fmt.Errorf("read from %s: %w", srcCollection, err)
		}
		uuid := doc.Ref.ID
		data := doc.Data()

		// Check if the document already exists in "journal" to ensure idempotency.
		destRef := client.Collection(destCollection).Doc(uuid)
		_, checkErr := destRef.Get(ctx)
		if checkErr == nil {
			// Document already present — skip.
			skipped++
			continue
		}
		if status.Code(checkErr) != codes.NotFound {
			log.Printf("WARN: check journal/%s: %v", uuid, checkErr)
			continue
		}

		// Enrich data for log entries.
		if forceNodeType != "" {
			data["node_type"] = forceNodeType
		}
		if _, ok := data["significance_weight"]; !ok {
			if forceNodeType == "log" {
				data["significance_weight"] = 0.3
			} else {
				data["significance_weight"] = 0.5
			}
		}
		if _, ok := data["timestamp"]; !ok {
			data["timestamp"] = time.Now().Format(time.RFC3339)
		}

		if dryRun {
			nt, _ := data["node_type"].(string)
			content, _ := data["content"].(string)
			preview := content
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			fmt.Printf("[dry-run] would copy %s/%s -> journal/%s (node_type=%s) %q\n", srcCollection, uuid, uuid, nt, preview)
			copied++
			continue
		}

		if _, err := destRef.Set(ctx, data); err != nil {
			log.Printf("WARN: write journal/%s: %v", uuid, err)
			continue
		}
		copied++
		if copied%100 == 0 {
			log.Printf("Progress: copied %d documents from %s...", copied, srcCollection)
		}
	}

	log.Printf("migrateCollection: src=%s dest=journal copied=%d skipped=%d", srcCollection, copied, skipped)
	return copied, nil
}
