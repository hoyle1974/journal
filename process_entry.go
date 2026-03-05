package jot

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/utils"
)

// ProcessEntry runs evaluator, context detection, journal analysis, and embedding for an entry.
func ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) error {
	LoggerFrom(ctx).Info("process-entry start", "entry_uuid", entryUUID, "content", utils.TruncateString(content, 50), "source", source)
	RunEvaluator(ctx, content, entryUUID, timestamp)

	contextUUIDs, err := DetectOrCreateContext(ctx, content, entryUUID)
	if err != nil {
		LoggerFrom(ctx).Warn("context detection failed", "error", err)
	}
	contextCount := len(contextUUIDs)

	analysis, err := AnalyzeJournalEntry(ctx, content, entryUUID, timestamp)
	if err != nil {
		LoggerFrom(ctx).Warn("journal analysis failed", "entry_uuid", entryUUID, "error", err)
	}
	var analysisJSON string
	if analysis != nil {
		if b, err := json.Marshal(analysis); err == nil {
			analysisJSON = string(b)
		}
	}

	vector, err := GenerateEmbedding(ctx, content, EmbedTaskRetrievalDocument)
	if err != nil {
		LoggerFrom(ctx).Warn("failed to generate entry embedding", "entry_uuid", entryUUID, "error", err)
		return fmt.Errorf("embedding: %w", err)
	}
	LoggerFrom(ctx).Debug("process-entry embedding generated", "entry_uuid", entryUUID, "dimensions", len(vector))

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		LoggerFrom(ctx).Warn("failed to get firestore for entry embedding", "error", err)
		return err
	}
	updates := []firestore.Update{{Path: "embedding", Value: firestore.Vector32(vector)}}
	if analysisJSON != "" {
		updates = append(updates, firestore.Update{Path: "journal_analysis", Value: analysisJSON})
	}
	_, err = client.Collection(EntriesCollection).Doc(entryUUID).Update(ctx, updates)
	if err != nil {
		LoggerFrom(ctx).Warn("failed to store entry embedding", "entry_uuid", entryUUID, "error", err)
		return err
	}
	LoggerFrom(ctx).Info("process-entry done", "entry_uuid", entryUUID, "contexts_linked", contextCount, "embedding_dims", len(vector), "has_analysis", analysisJSON != "")
	return nil
}
