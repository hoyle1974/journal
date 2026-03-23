package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// firestoreMaxBatchSize is Firestore's hard limit on writes per batch commit.
const firestoreMaxBatchSize = 500

// firestoreIntValue is satisfied by *firestorepb.Value returned in aggregation results.
type firestoreIntValue interface{ GetIntegerValue() int64 }

// errSkipEntry is a sentinel returned from mapDoc callbacks to exclude a document
// from results without treating it as an error.
var errSkipEntry = errors.New("skip entry")

// padDateStart appends T00:00:00 to a bare date (YYYY-MM-DD) for timestamp comparisons.
func padDateStart(d string) string {
	if len(d) == 10 {
		return d + "T00:00:00"
	}
	return d
}

// padDateEnd appends T23:59:59 to a bare date (YYYY-MM-DD) for timestamp comparisons.
func padDateEnd(d string) string {
	if len(d) == 10 {
		return d + "T23:59:59"
	}
	return d
}

// Entry represents a journal entry (episodic log node).
type Entry struct {
	UUID                   string `firestore:"-" json:"uuid"`
	Content                string `firestore:"content" json:"content"`
	Source                 string `firestore:"source" json:"source"`
	Timestamp              string `firestore:"timestamp" json:"timestamp"`
	ImageURL               string `firestore:"image_url,omitempty" json:"image_url,omitempty"`
	ParsedImageDescription string `firestore:"parsed_image_description,omitempty" json:"parsed_image_description,omitempty"`
	AudioURL               string `firestore:"audio_url,omitempty" json:"audio_url,omitempty"`
	Transcription          string `firestore:"transcription,omitempty" json:"transcription,omitempty"`
}

// AddEntry writes a new entry to Firestore and returns the entry UUID.
// Caller is responsible for enqueueing process-entry (e.g. in jot).
// imageURL is optional (e.g. gs://bucket/path); when non-empty it is stored on the entry.
func (s *Store) AddEntry(ctx context.Context, content, source string, timestamp *string, imageURL string) (string, error) {
	if content == "" {
		return "", fmt.Errorf("content is required and must be a non-empty string")
	}
	if source == "" {
		return "", fmt.Errorf("source is required and must be a string")
	}

	entryUUID := generateUUID()
	ts := time.Now().Format(time.RFC3339)
	if timestamp != nil && *timestamp != "" {
		ts = *timestamp
	}

	doc := map[string]interface{}{
		"content":             content,
		"source":              source,
		"timestamp":           ts,
		"node_type":           NodeTypeLog,
		"significance_weight": 0.3,
	}
	if imageURL != "" {
		doc["image_url"] = imageURL
	}
	_, err := s.db.Collection(KnowledgeCollection).Doc(entryUUID).Set(ctx, doc)
	if err != nil {
		return "", err
	}

	s.log.Debug("entry written to Firestore", "uuid", entryUUID, "source", source, "content", content, "image_url", imageURL)
	return entryUUID, nil
}

// UpdateEntryAudio sets the audio_url and transcription fields on an existing entry.
// Call after transcription completes so the entry reflects both the stored audio and its text.
func (s *Store) UpdateEntryAudio(ctx context.Context, entryUUID, audioURL, transcription string) error {
	updates := []firestore.Update{
		{Path: "audio_url", Value: audioURL},
		{Path: "transcription", Value: transcription},
		// Replace placeholder content with the actual transcription.
		{Path: "content", Value: transcription},
	}
	_, err := s.db.Collection(KnowledgeCollection).Doc(entryUUID).Update(ctx, updates)
	return err
}

func (s *Store) getEntriesOrdered(ctx context.Context, limit int, dir firestore.Direction) ([]Entry, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeLog).
		OrderBy("timestamp", dir).
		Limit(limit)
	return queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, fmt.Errorf("decode entry: %w", err)
		}
		e.UUID = doc.Ref.ID
		return e, nil
	})
}

// GetEntries fetches entries from Firestore, ordered by timestamp descending.
func (s *Store) GetEntries(ctx context.Context, limit int) ([]Entry, error) {
	return s.getEntriesOrdered(ctx, limit, firestore.Desc)
}

// GetEntriesAsc fetches entries from Firestore, ordered by timestamp ascending (oldest first).
func (s *Store) GetEntriesAsc(ctx context.Context, limit int) ([]Entry, error) {
	return s.getEntriesOrdered(ctx, limit, firestore.Asc)
}

// GetAllLogEntries fetches every node_type="log" entry sorted ascending by timestamp.
// No limit is applied — intended for admin export operations.
func (s *Store) GetAllLogEntries(ctx context.Context) ([]Entry, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeLog).
		OrderBy("timestamp", firestore.Asc)
	return queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, fmt.Errorf("decode entry: %w", err)
		}
		e.UUID = doc.Ref.ID
		return e, nil
	})
}

// GetEntriesByDateRange fetches entries within a date range.
func (s *Store) GetEntriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]Entry, error) {
	startDate = padDateStart(startDate)
	endDate = padDateEnd(endDate)
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeLog).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	entries, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, err
		}
		e.UUID = doc.Ref.ID
		return e, nil
	})
	if err != nil {
		return nil, wrapFirestoreIndexError(err)
	}
	return entries, nil
}

// SearchEntries searches entries containing keywords (case-insensitive).
func (s *Store) SearchEntries(ctx context.Context, keywords string, limit int) ([]Entry, error) {
	keywordsLower := strings.Fields(strings.ToLower(keywords))
	fetchLimit := limit * 5
	if fetchLimit < 50 {
		fetchLimit = 50
	}
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeLog).
		OrderBy("timestamp", firestore.Desc).
		Limit(fetchLimit)
	entries, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, err
		}
		contentLower := strings.ToLower(e.Content)
		for _, kw := range keywordsLower {
			if !strings.Contains(contentLower, kw) {
				return Entry{}, errSkipEntry
			}
		}
		e.UUID = doc.Ref.ID
		return e, nil
	})
	if err != nil {
		return nil, err
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// CountEntries counts entries, optionally within a date range.
func (s *Store) CountEntries(ctx context.Context, startDate, endDate *string) (int, error) {
	var query firestore.Query
	if startDate != nil && endDate != nil && *startDate != "" && *endDate != "" {
		start := padDateStart(*startDate)
		end := padDateEnd(*endDate)
		query = s.db.Collection(KnowledgeCollection).
			Where("node_type", "==", NodeTypeLog).
			Where("timestamp", ">=", start).
			Where("timestamp", "<=", end)
	} else {
		query = s.db.Collection(KnowledgeCollection).
			Where("node_type", "==", NodeTypeLog)
	}
	result, err := query.NewAggregationQuery().WithCount("count").Get(ctx)
	if err != nil {
		return 0, wrapFirestoreIndexError(err)
	}
	val, ok := result["count"]
	if !ok {
		return 0, fmt.Errorf("count key missing from aggregation result")
	}
	// The SDK stores aggregation values as *firestorepb.Value; use GetIntegerValue().
	if g, ok := val.(firestoreIntValue); ok {
		return int(g.GetIntegerValue()), nil
	}
	// Fallback for direct int64 (future SDK changes).
	if n, ok := val.(int64); ok {
		return int(n), nil
	}
	return 0, fmt.Errorf("unexpected count result type: %T", val)
}

// GetUniqueSources returns all unique source values from entries.
func (s *Store) GetUniqueSources(ctx context.Context) ([]string, error) {
	iter := s.db.Collection(KnowledgeCollection).Where("node_type", "==", NodeTypeLog).Select("source").Limit(1000).Documents(ctx)
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

// GetEntriesBySource returns entries filtered by source (partial match).
func (s *Store) GetEntriesBySource(ctx context.Context, sourceFilter string, limit int) ([]Entry, error) {
	sourceFilterLower := strings.ToLower(sourceFilter)
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeLog).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit * 5)
	entries, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, err
		}
		if !strings.Contains(strings.ToLower(e.Source), sourceFilterLower) {
			return Entry{}, errSkipEntry
		}
		e.UUID = doc.Ref.ID
		return e, nil
	})
	if err != nil {
		return nil, err
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// GetEntry fetches a single entry by UUID.
func (s *Store) GetEntry(ctx context.Context, entryUUID string) (*Entry, error) {
	doc, err := s.db.Collection(KnowledgeCollection).Doc(entryUUID).Get(ctx)
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

// GetEntryDates returns a map from entry UUID to date string (YYYY-MM-DD). Missing entries are omitted.
func (s *Store) GetEntryDates(ctx context.Context, entryIDs []string) (map[string]string, error) {
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
		e, err := s.GetEntry(ctx, id)
		if err != nil || e == nil || e.Timestamp == "" {
			continue
		}
		if len(e.Timestamp) >= 10 {
			result[id] = e.Timestamp[:10]
		} else {
			result[id] = e.Timestamp
		}
	}
	return result, nil
}

// UpdateEntry updates an entry's content.
func (s *Store) UpdateEntry(ctx context.Context, entryUUID, newContent string) error {
	_, err := s.db.Collection(KnowledgeCollection).Doc(entryUUID).Update(ctx, []firestore.Update{
		{Path: "content", Value: newContent},
	})
	return err
}

// DeleteEntry deletes a single entry.
func (s *Store) DeleteEntry(ctx context.Context, entryUUID string) error {
	_, err := s.db.Collection(KnowledgeCollection).Doc(entryUUID).Delete(ctx)
	return err
}

// DeleteEntries deletes multiple entries.
func (s *Store) DeleteEntries(ctx context.Context, entryUUIDs []string) error {
	if len(entryUUIDs) == 0 {
		return nil
	}
	for i := 0; i < len(entryUUIDs); i += firestoreMaxBatchSize {
		end := min(i+firestoreMaxBatchSize, len(entryUUIDs))
		batch := s.db.Batch()
		for _, uuid := range entryUUIDs[i:end] {
			batch.Delete(s.db.Collection(KnowledgeCollection).Doc(uuid))
		}
		if _, err := batch.Commit(ctx); err != nil {
			return fmt.Errorf("delete entries batch: %w", err)
		}
	}
	return nil
}

// GetDatesWithEntries returns sorted dates (YYYY-MM-DD) that have at least one entry.
func (s *Store) GetDatesWithEntries(ctx context.Context) ([]string, error) {
	iter := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeLog).
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
