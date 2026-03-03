// Package memory: migration of knowledge node metadata to normalized schema.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// MigrateKnowledgeMetadata iterates over all documents in the given Firestore collection
// (knowledge_nodes), normalizes metadata for registered node types, and merges top-level
// archive_summary into metadata for project/goal nodes. When dryRun is true, no writes
// are performed. Returns the number of documents updated and any error.
func MigrateKnowledgeMetadata(ctx context.Context, client *firestore.Client, collection string, dryRun bool) (int, error) {
	iter := client.Collection(collection).Documents(ctx)
	defer iter.Stop()

	updated := 0
	batch := client.Batch()
	batchCount := 0
	const batchSize = 500

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return updated, fmt.Errorf("iterate documents: %w", err)
		}

		data := doc.Data()
		nodeType := getStringFromData(data, "node_type")
		metadataStr := getStringFromData(data, "metadata")
		topLevelArchive := getStringFromData(data, "archive_summary")

		var m map[string]any
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &m); err != nil {
				// Unparseable: treat as empty and optionally store as generic
				m = make(map[string]any)
			}
		}
		if m == nil {
			m = make(map[string]any)
		}

		// Merge top-level archive_summary into metadata for project/goal
		if topLevelArchive != "" && (nodeType == NodeTypeProject || nodeType == NodeTypeGoal) {
			current, _ := m["archive_summary"].(string)
			if current == "" {
				m["archive_summary"] = topLevelArchive
			} else {
				m["archive_summary"] = topLevelArchive + "\n" + current
			}
		}

		var newMetadata string
		var shouldUpdate bool
		if IsRegistered(nodeType) {
			normalized, err := NormalizeMetadata(nodeType, m)
			if err != nil {
				log.Printf("skip %s: normalize: %v", doc.Ref.ID, err)
				continue
			}
			newMetadata, err = MetadataToJSON(normalized)
			if err != nil {
				log.Printf("skip %s: marshal: %v", doc.Ref.ID, err)
				continue
			}
			shouldUpdate = newMetadata != metadataStr
		} else {
			// Unregistered type: only update when merging top-level archive_summary
			if topLevelArchive != "" && (nodeType == NodeTypeProject || nodeType == NodeTypeGoal) {
				b, err := json.Marshal(m)
				if err != nil {
					continue
				}
				newMetadata = string(b)
				shouldUpdate = true
			} else {
				continue
			}
		}

		if !shouldUpdate && topLevelArchive == "" {
			continue
		}

		updated++
		if dryRun {
			log.Printf("[dry-run] would update %s (node_type=%s)", doc.Ref.ID, nodeType)
			continue
		}

		updates := []firestore.Update{{Path: "metadata", Value: newMetadata}}
		if topLevelArchive != "" && (nodeType == NodeTypeProject || nodeType == NodeTypeGoal) {
			updates = append(updates, firestore.Update{Path: "archive_summary", Value: firestore.Delete})
		}
		batch.Update(doc.Ref, updates)
		batchCount++

		if batchCount >= batchSize {
			if _, err := batch.Commit(ctx); err != nil {
				return updated, fmt.Errorf("batch commit: %w", err)
			}
			batch = client.Batch()
			batchCount = 0
		}
	}

	if !dryRun && batchCount > 0 {
		if _, err := batch.Commit(ctx); err != nil {
			return updated, fmt.Errorf("batch commit: %w", err)
		}
	}

	return updated, nil
}

func getStringFromData(data map[string]interface{}, field string) string {
	if data == nil {
		return ""
	}
	v, ok := data[field]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
