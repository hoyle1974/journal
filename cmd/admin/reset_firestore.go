// reset_firestore deletes all documents from the Firestore collections used by Jot.
// Requires the user to confirm by typing a random 3-digit number.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"strings"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/task"
)

// firestoreCollections lists all Firestore collection names used by the app.
var firestoreCollections = []string{
	memory.KnowledgeCollection,   // knowledge_nodes
	task.TasksCollection,         // tasks
	infra.SystemCollection,       // _system
	memory.PendingQuestionsCollection, // pending_questions
	journal.EntriesCollection,    // entries
	journal.QueriesCollection,    // queries
}

func runResetFirestore(ctx context.Context, app *infra.App, _ []string) {
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		log.Fatal("GOOGLE_CLOUD_PROJECT is not set (source .env or .env.prod first).")
	}
	code := 100 + rand.IntN(900) // 100–999
	fmt.Fprintf(os.Stderr, "This will DELETE ALL DATA in Firestore for project: %s\n", project)
	fmt.Fprintf(os.Stderr, "To confirm, type this number: %d\n", code)
	fmt.Fprint(os.Stderr, "> ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("reading confirmation: %v", err)
	}
	trimmed := strings.TrimSpace(line)
	if trimmed != fmt.Sprintf("%d", code) {
		log.Fatal("Confirmation did not match. Aborted.")
	}

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("Firestore: %v", err)
	}

	for _, collName := range firestoreCollections {
		n, err := deleteCollection(ctx, client, collName)
		if err != nil {
			log.Fatalf("delete %s: %v", collName, err)
		}
		fmt.Printf("Deleted %d documents from %s\n", n, collName)
	}
	fmt.Println("Firestore reset complete.")
}

func deleteCollection(ctx context.Context, client *firestore.Client, collectionName string) (int, error) {
	const batchSize = 500
	count := 0
	for {
		iter := client.Collection(collectionName).Limit(batchSize).Documents(ctx)
		batch := client.Batch()
		n := 0
		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				iter.Stop()
				return count, err
			}
			batch.Delete(doc.Ref)
			n++
		}
		iter.Stop()
		if n == 0 {
			break
		}
		if _, err := batch.Commit(ctx); err != nil {
			return count, err
		}
		count += n
		if n < batchSize {
			break
		}
	}
	return count, nil
}
