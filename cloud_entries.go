package jot

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// =============================================================================
// ENTRY TYPES
// =============================================================================

// Entry represents a journal entry.
type Entry struct {
	UUID      string `firestore:"-" json:"uuid"`
	Content   string `firestore:"content" json:"content"`
	Source    string `firestore:"source" json:"source"`
	Timestamp string `firestore:"timestamp" json:"timestamp"`
}

// =============================================================================
// ENTRY OPERATIONS
// =============================================================================

// AddEntry adds a new entry to Firestore. Returns the entry UUID.
func AddEntry(ctx context.Context, content, source string, timestamp *string) (string, error) {
	if content == "" {
		return "", fmt.Errorf("content is required and must be a non-empty string")
	}
	if source == "" {
		return "", fmt.Errorf("source is required and must be a string")
	}

	entryUUID := GenerateUUID()
	ts := time.Now().Format(time.RFC3339)
	if timestamp != nil && *timestamp != "" {
		ts = *timestamp
	}

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return "", err
	}

	_, err = client.Collection(EntriesCollection).Doc(entryUUID).Set(ctx, map[string]interface{}{
		"content":   content,
		"source":    source,
		"timestamp": ts,
	})
	if err != nil {
		return "", err
	}

	payload := map[string]interface{}{
		"uuid":      entryUUID,
		"content":   content,
		"timestamp": ts,
		"source":    source,
	}
	if err := EnqueueTask(ctx, "/internal/process-entry", payload); err != nil {
		LoggerFrom(ctx).Warn("failed to enqueue process-entry task, running inline", "entry_uuid", entryUUID, "error", err)
		// Run process-entry inline (e.g. when JOT_API_URL is not set or Cloud Tasks unavailable)
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
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var entries []Entry
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var e Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}
		e.UUID = doc.Ref.ID
		entries = append(entries, e)
	}
	return entries, nil
}

// GetEntriesAsc fetches entries from Firestore, ordered by timestamp ascending (oldest first).
// Use this when the user asks for the "oldest" or "earliest" entry or memory.
func GetEntriesAsc(ctx context.Context, limit int) ([]Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Asc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var entries []Entry
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var e Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}
		e.UUID = doc.Ref.ID
		entries = append(entries, e)
	}
	return entries, nil
}

// GetEntriesByDateRange fetches entries within a date range.
func GetEntriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	// Normalize dates to include full day
	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}

	iter := client.Collection(EntriesCollection).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var entries []Entry
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, WrapFirestoreIndexError(err)
		}

		var e Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}
		e.UUID = doc.Ref.ID
		entries = append(entries, e)
	}
	return entries, nil
}

// SearchEntries searches entries containing keywords (case-insensitive).
func SearchEntries(ctx context.Context, keywords string, limit int) ([]Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(500).
		Documents(ctx)
	defer iter.Stop()

	keywordsLower := strings.Fields(strings.ToLower(keywords))
	var entries []Entry

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var e Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}

		contentLower := strings.ToLower(e.Content)
		allMatch := true
		for _, kw := range keywordsLower {
			if !strings.Contains(contentLower, kw) {
				allMatch = false
				break
			}
		}

		if allMatch {
			e.UUID = doc.Ref.ID
			entries = append(entries, e)
			if len(entries) >= limit {
				break
			}
		}
	}
	return entries, nil
}

// CountEntries counts entries, optionally within a date range.
func CountEntries(ctx context.Context, startDate, endDate *string) (int, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return 0, err
	}

	var query firestore.Query
	if startDate != nil && endDate != nil && *startDate != "" && *endDate != "" {
		start := *startDate
		end := *endDate
		if len(start) == 10 {
			start = start + "T00:00:00"
		}
		if len(end) == 10 {
			end = end + "T23:59:59"
		}
		query = client.Collection(EntriesCollection).
			Where("timestamp", ">=", start).
			Where("timestamp", "<=", end)
	} else {
		query = client.Collection(EntriesCollection).Query
	}

	iter := query.Documents(ctx)
	defer iter.Stop()

	count := 0
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, WrapFirestoreIndexError(err)
		}
		count++
	}
	return count, nil
}

// GetUniqueSources gets all unique sources from entries.
func GetUniqueSources(ctx context.Context) ([]string, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).Limit(1000).Documents(ctx)
	defer iter.Stop()

	sources := make(map[string]bool)
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		data := doc.Data()
		if source, ok := data["source"].(string); ok && source != "" {
			sources[source] = true
		}
	}

	result := make([]string, 0, len(sources))
	for s := range sources {
		result = append(result, s)
	}
	sort.Strings(result)
	return result, nil
}

// GetEntriesBySource gets entries filtered by source (partial match).
func GetEntriesBySource(ctx context.Context, sourceFilter string, limit int) ([]Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(500).
		Documents(ctx)
	defer iter.Stop()

	sourceFilterLower := strings.ToLower(sourceFilter)
	var entries []Entry

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var e Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}

		if strings.Contains(strings.ToLower(e.Source), sourceFilterLower) {
			e.UUID = doc.Ref.ID
			entries = append(entries, e)
			if len(entries) >= limit {
				break
			}
		}
	}
	return entries, nil
}

// GetEntry fetches a single entry by UUID.
func GetEntry(ctx context.Context, entryUUID string) (*Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	doc, err := client.Collection(EntriesCollection).Doc(entryUUID).Get(ctx)
	if err != nil {
		return nil, err
	}

	var e Entry
	if err := doc.DataTo(&e); err != nil {
		return nil, err
	}
	e.UUID = doc.Ref.ID
	return &e, nil
}

// GetEntryDates fetches timestamps for the given entry UUIDs and returns a map from UUID to date string (YYYY-MM-DD).
// Missing or failed entries are omitted. Used for source receipt display (e.g. "Source: 2026-02-15").
func GetEntryDates(ctx context.Context, entryIDs []string) (map[string]string, error) {
	if len(entryIDs) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	deduped := make([]string, 0, len(entryIDs))
	for _, id := range entryIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			deduped = append(deduped, id)
		}
	}
	result := make(map[string]string, len(deduped))
	for _, id := range deduped {
		e, err := GetEntry(ctx, id)
		if err != nil || e == nil || e.Timestamp == "" {
			continue
		}
		date := e.Timestamp
		if len(date) > 10 {
			date = date[:10]
		}
		result[id] = date
	}
	return result, nil
}

// UpdateEntry updates an entry's content.
func UpdateEntry(ctx context.Context, entryUUID, newContent string) error {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}

	_, err = client.Collection(EntriesCollection).Doc(entryUUID).Update(ctx, []firestore.Update{
		{Path: "content", Value: newContent},
	})
	return err
}

// DeleteEntry deletes a single entry.
func DeleteEntry(ctx context.Context, entryUUID string) error {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}

	_, err = client.Collection(EntriesCollection).Doc(entryUUID).Delete(ctx)
	return err
}

// DeleteEntries deletes multiple entries.
func DeleteEntries(ctx context.Context, entryUUIDs []string) error {
	if len(entryUUIDs) == 0 {
		return nil
	}

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}

	batch := client.Batch()
	for _, uuid := range entryUUIDs {
		batch.Delete(client.Collection(EntriesCollection).Doc(uuid))
	}
	_, err = batch.Commit(ctx)
	return err
}

// GetDatesWithEntries gets list of dates that have entries.
func GetDatesWithEntries(ctx context.Context) ([]string, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Asc).
		Documents(ctx)
	defer iter.Stop()

	dates := make(map[string]bool)
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		data := doc.Data()
		if ts, ok := data["timestamp"].(string); ok && len(ts) >= 10 {
			dates[ts[:10]] = true
		}
	}

	result := make([]string, 0, len(dates))
	for d := range dates {
		result = append(result, d)
	}
	sort.Strings(result)
	return result, nil
}

// QuerySimilarEntries performs a KNN vector search on journal entries.
// Requires a Firestore vector index on entries.embedding (768 dimensions).
func QuerySimilarEntries(ctx context.Context, queryVector []float32, limit int) ([]Entry, error) {
	ctx, span := StartSpan(ctx, "entries.query_similar")
	defer span.End()

	client, err := GetFirestoreClient(ctx)
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
		e := Entry{
			UUID:      doc.Ref.ID,
			Content:   getStringField(data, "content"),
			Source:    getStringField(data, "source"),
			Timestamp: getStringField(data, "timestamp"),
		}
		entries = append(entries, e)
	}

	span.SetAttributes(map[string]string{
		"results_count": fmt.Sprintf("%d", len(entries)),
	})
	return entries, nil
}

// BackfillEntryEmbeddings finds entries without embeddings, generates them, and updates docs.
// Processes up to limit entries per call. Returns number processed and any error.
func BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error) {
	ctx, span := StartSpan(ctx, "entries.backfill_embeddings")
	defer span.End()

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	client, err := GetFirestoreClient(ctx)
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
