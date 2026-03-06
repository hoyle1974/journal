package service

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

// ProcessEntry runs evaluator, context detection, journal analysis, and embedding for an entry.
func ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) error {
	infra.LoggerFrom(ctx).Info("process-entry start", "entry_uuid", entryUUID, "content", utils.TruncateString(content, 50), "source", source)
	infra.LoggerFrom(ctx).Debug("process-entry: running evaluator", "entry_uuid", entryUUID, "reason", "extract significance and optionally store fact")
	agent.RunEvaluator(ctx, ServiceEnv{}, content, entryUUID, timestamp)

	contextUUIDs, err := memory.DetectOrCreateContext(ctx, content, entryUUID)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("context detection failed", "error", err)
	}
	contextCount := len(contextUUIDs)
	infra.LoggerFrom(ctx).Debug("process-entry: context detection done", "entry_uuid", entryUUID, "contexts_linked", contextCount, "reason", "link entry to active contexts")

	analysis, err := journal.AnalyzeJournalEntry(ctx, content, entryUUID, timestamp)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("journal analysis failed", "entry_uuid", entryUUID, "error", err)
	}
	var analysisJSON string
	if analysis != nil {
		if b, err := json.Marshal(analysis); err == nil {
			analysisJSON = string(b)
		}
	}
	infra.LoggerFrom(ctx).Debug("process-entry: journal analysis done", "entry_uuid", entryUUID, "has_analysis", analysis != nil, "reason", "mood/tags/entities for rollup and search")

	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return fmt.Errorf("no app config for embedding")
	}
	vector, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, content, infra.EmbedTaskRetrievalDocument)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("failed to generate entry embedding", "entry_uuid", entryUUID, "error", err)
		return fmt.Errorf("embedding: %w", err)
	}
	infra.LoggerFrom(ctx).Debug("process-entry embedding generated", "entry_uuid", entryUUID, "dimensions", len(vector), "reason", "for semantic search")

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("failed to get firestore for entry embedding", "error", err)
		return err
	}
	updates := []firestore.Update{{Path: "embedding", Value: firestore.Vector32(vector)}}
	if analysisJSON != "" {
		updates = append(updates, firestore.Update{Path: "journal_analysis", Value: analysisJSON})
	}
	infra.LoggerFrom(ctx).Debug("process-entry: writing embedding and analysis to Firestore", "entry_uuid", entryUUID, "reason", "persist for RAG and rollups")
	_, err = client.Collection(journal.EntriesCollection).Doc(entryUUID).Update(ctx, updates)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("failed to store entry embedding", "entry_uuid", entryUUID, "error", err)
		return err
	}
	infra.LoggerFrom(ctx).Info("process-entry done", "entry_uuid", entryUUID, "contexts_linked", contextCount, "embedding_dims", len(vector), "has_analysis", analysisJSON != "")
	infra.LoggerFrom(ctx).Debug("process-entry: done", "entry_uuid", entryUUID, "reason", "evaluator, context links, analysis, and embedding all completed")
	return nil
}
