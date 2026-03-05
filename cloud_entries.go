package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"google.golang.org/api/iterator"
)

// Entry is a journal entry. Re-exported from journal.
type Entry = journal.Entry

// EntryWithAnalysis pairs an entry with its parsed journal_analysis (when present).
type EntryWithAnalysis struct {
	Entry    journal.Entry
	Analysis *JournalAnalysis
}

// AddEntry adds a new entry to Firestore and enqueues process-entry. Returns the entry UUID.
func AddEntry(ctx context.Context, content, source string, timestamp *string) (string, error) {
	entryUUID, err := journal.AddEntry(ctx, content, source, timestamp)
	if err != nil {
		return "", err
	}
	ts := time.Now().Format(time.RFC3339)
	if timestamp != nil && *timestamp != "" {
		ts = *timestamp
	}
	payload := map[string]interface{}{
		"uuid":      entryUUID,
		"content":   content,
		"timestamp": ts,
		"source":    source,
	}
	if err := EnqueueTask(ctx, "/internal/process-entry", payload); err != nil {
		LoggerFrom(ctx).Warn("failed to enqueue process-entry task, running inline", "entry_uuid", entryUUID, "error", err)
		app := GetApp(ctx)
		if app != nil {
			bgCtx := WithApp(context.Background(), app)
			SubmitAsync(ctx, func() {
				_ = ProcessEntry(bgCtx, entryUUID, content, ts, source)
			})
		}
	}
	return entryUUID, nil
}

// GetEntries fetches entries from Firestore, ordered by timestamp descending.
func GetEntries(ctx context.Context, limit int) ([]Entry, error) {
	return journal.GetEntries(ctx, limit)
}

// GetEntriesAsc fetches entries from Firestore, ordered by timestamp ascending (oldest first).
func GetEntriesAsc(ctx context.Context, limit int) ([]Entry, error) {
	return journal.GetEntriesAsc(ctx, limit)
}

// GetEntriesByDateRange fetches entries within a date range.
func GetEntriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]Entry, error) {
	return journal.GetEntriesByDateRange(ctx, startDate, endDate, limit)
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
		e := journal.Entry{
			UUID:      doc.Ref.ID,
			Content:   getStringField(data, "content"),
			Source:    getStringField(data, "source"),
			Timestamp: getStringField(data, "timestamp"),
		}
		var analysis *JournalAnalysis
		if raw := getStringField(data, "journal_analysis"); raw != "" {
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
		return nil, WrapFirestoreIndexError(err)
	}
	return result, nil
}

// SearchEntries searches entries containing keywords (case-insensitive).
func SearchEntries(ctx context.Context, keywords string, limit int) ([]Entry, error) {
	return journal.SearchEntries(ctx, keywords, limit)
}

// CountEntries counts entries, optionally within a date range.
func CountEntries(ctx context.Context, startDate, endDate *string) (int, error) {
	return journal.CountEntries(ctx, startDate, endDate)
}

// GetUniqueSources returns all unique source values from entries.
func GetUniqueSources(ctx context.Context) ([]string, error) {
	return journal.GetUniqueSources(ctx)
}

// GetEntriesBySource returns entries filtered by source (partial match).
func GetEntriesBySource(ctx context.Context, sourceFilter string, limit int) ([]Entry, error) {
	return journal.GetEntriesBySource(ctx, sourceFilter, limit)
}

// GetEntry fetches a single entry by UUID.
func GetEntry(ctx context.Context, entryUUID string) (*Entry, error) {
	return journal.GetEntry(ctx, entryUUID)
}

// GetEntryDates fetches timestamps for the given entry UUIDs (map UUID -> YYYY-MM-DD). Used for source receipt.
func GetEntryDates(ctx context.Context, entryIDs []string) (map[string]string, error) {
	return journal.GetEntryDates(ctx, entryIDs)
}

// UpdateEntry updates an entry's content.
func UpdateEntry(ctx context.Context, entryUUID, newContent string) error {
	return journal.UpdateEntry(ctx, entryUUID, newContent)
}

// DeleteEntry deletes a single entry.
func DeleteEntry(ctx context.Context, entryUUID string) error {
	return journal.DeleteEntry(ctx, entryUUID)
}

// DeleteEntries deletes multiple entries.
func DeleteEntries(ctx context.Context, entryUUIDs []string) error {
	return journal.DeleteEntries(ctx, entryUUIDs)
}

// GetDatesWithEntries returns sorted dates (YYYY-MM-DD) that have at least one entry.
func GetDatesWithEntries(ctx context.Context) ([]string, error) {
	return journal.GetDatesWithEntries(ctx)
}

// QuerySimilarEntries performs a KNN vector search on journal entries.
func QuerySimilarEntries(ctx context.Context, queryVector []float32, limit int) ([]Entry, error) {
	ctx, span := StartSpan(ctx, "entries.query_similar")
	defer span.End()

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	vectorQuery := client.Collection(EntriesCollection).
		FindNearest("embedding", firestore.Vector32(queryVector), limit, firestore.DistanceMeasureCosine, nil)

	iter := vectorQuery.Documents(ctx)
	defer iter.Stop()

	var entries []Entry
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			LoggerFrom(ctx).Error("entry vector search error", "error", err)
			span.RecordError(err)
			return nil, err
		}

		data := doc.Data()
		entries = append(entries, Entry{
			UUID:      doc.Ref.ID,
			Content:   getStringField(data, "content"),
			Source:    getStringField(data, "source"),
			Timestamp: getStringField(data, "timestamp"),
		})
	}

	span.SetAttributes(map[string]string{
		"results_count": fmt.Sprintf("%d", len(entries)),
	})
	return entries, nil
}

// BackfillEntryEmbeddings finds entries without embeddings, generates them, and updates docs.
func BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error) {
	ctx, span := StartSpan(ctx, "entries.backfill_embeddings")
	defer span.End()

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return 0, err
	}

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

		content := getStringField(data, "content")
		if content == "" {
			continue
		}

		vector, err := GenerateEmbedding(ctx, content, EmbedTaskRetrievalDocument)
		if err != nil {
			LoggerFrom(ctx).Warn("backfill embedding failed", "doc", doc.Ref.ID, "error", err)
			continue
		}

		_, err = client.Collection(EntriesCollection).Doc(doc.Ref.ID).Update(ctx, []firestore.Update{
			{Path: "embedding", Value: firestore.Vector32(vector)},
		})
		if err != nil {
			LoggerFrom(ctx).Warn("backfill update failed", "doc", doc.Ref.ID, "error", err)
			continue
		}
		processed++
		LoggerFrom(ctx).Debug("backfill embedded entry", "doc", doc.Ref.ID)
	}

	span.SetAttributes(map[string]string{"processed": fmt.Sprintf("%d", processed)})
	return processed, nil
}
