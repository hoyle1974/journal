package journal

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/infra"
	"google.golang.org/api/iterator"
)

// EntryWithAnalysis pairs an entry with its parsed journal_analysis (when present).
type EntryWithAnalysis struct {
	Entry    Entry
	Analysis *JournalAnalysis
}

// GetEntriesWithAnalysisByDateRange fetches entries in the date range and parses journal_analysis from each doc.
func GetEntriesWithAnalysisByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]EntryWithAnalysis, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}
	query := client.Collection(EntriesCollection).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	result, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (EntryWithAnalysis, error) {
		data := doc.Data()
		e := Entry{
			UUID:      doc.Ref.ID,
			Content:   infra.GetStringField(data, "content"),
			Source:    infra.GetStringField(data, "source"),
			Timestamp: infra.GetStringField(data, "timestamp"),
		}
		var analysis *JournalAnalysis
		if raw := infra.GetStringField(data, "journal_analysis"); raw != "" {
			var a JournalAnalysis
			if jsonErr := json.Unmarshal([]byte(raw), &a); jsonErr == nil {
				a.SourceID = e.UUID
				for i := range a.Entities {
					if a.Entities[i].SourceID == "" {
						a.Entities[i].SourceID = e.UUID
					}
					a.Entities[i].Status = NormalizeEntityStatus(a.Entities[i].Status)
				}
				analysis = &a
			}
		}
		return EntryWithAnalysis{Entry: e, Analysis: analysis}, nil
	})
	if err != nil {
		return nil, infra.WrapFirestoreIndexError(err)
	}
	return result, nil
}

// QuerySimilarEntries performs a KNN vector search on journal entries.
func QuerySimilarEntries(ctx context.Context, queryVector []float32, limit int) ([]Entry, error) {
	ctx, span := infra.StartSpan(ctx, "entries.query_similar")
	defer span.End()

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	const distanceResultField = "_vector_distance"
	opts := &firestore.FindNearestOptions{DistanceResultField: distanceResultField}
	vectorQuery := client.Collection(EntriesCollection).
		FindNearest("embedding", firestore.Vector32(queryVector), limit, firestore.DistanceMeasureCosine, opts)
	iter := vectorQuery.Documents(ctx)
	defer iter.Stop()

	var entries []Entry
	var scores []float64
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			infra.LogVectorSearchFailed(ctx, EntriesCollection, err, 0)
			span.RecordError(err)
			return nil, err
		}
		data := doc.Data()
		content := infra.GetStringField(data, "content")
		entries = append(entries, Entry{
			UUID:      doc.Ref.ID,
			Content:   content,
			Source:    infra.GetStringField(data, "source"),
			Timestamp: infra.GetStringField(data, "timestamp"),
		})
		score := 0.0
		if v, ok := data[distanceResultField]; ok {
			switch x := v.(type) {
			case float64:
				score = 1 - x
			case float32:
				score = 1 - float64(x)
			}
			if score < 0 {
				score = 0
			}
			if score > 1 {
				score = 1
			}
		}
		scores = append(scores, score)
		textPreview := content
		if len(textPreview) > 50 {
			textPreview = textPreview[:47] + "..."
		}
		infra.LogFoundEntry(ctx, doc.Ref.ID, score, textPreview)
	}
	infra.LogRAGQuality(ctx, limit, scores)
	span.SetAttributes(map[string]string{"results_count": fmt.Sprintf("%d", len(entries))})
	return entries, nil
}

// BackfillEntryEmbeddings finds entries without embeddings, generates them, and updates docs.
func BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error) {
	ctx, span := infra.StartSpan(ctx, "entries.backfill_embeddings")
	defer span.End()

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return 0, err
	}
	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return 0, fmt.Errorf("no app in context")
	}
	projectID := app.Config().GoogleCloudProject

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Asc).
		Limit(500).
		Documents(ctx)
	defer iter.Stop()

	processed := 0
	for processed < limit {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return processed, err
		}
		data := doc.Data()
		if _, has := data["embedding"]; has {
			continue
		}
		content := infra.GetStringField(data, "content")
		if content == "" {
			continue
		}
		vector, err := infra.GenerateEmbedding(ctx, projectID, content, infra.EmbedTaskRetrievalDocument)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("backfill embedding failed", "doc", doc.Ref.ID, "error", err)
			continue
		}
		_, err = client.Collection(EntriesCollection).Doc(doc.Ref.ID).Update(ctx, []firestore.Update{
			{Path: "embedding", Value: firestore.Vector32(vector)},
		})
		if err != nil {
			infra.LoggerFrom(ctx).Warn("backfill update failed", "doc", doc.Ref.ID, "error", err)
			continue
		}
		processed++
		infra.LoggerFrom(ctx).Debug("backfill embedded entry", "doc", doc.Ref.ID)
	}
	span.SetAttributes(map[string]string{"processed": fmt.Sprintf("%d", processed)})
	return processed, nil
}
