package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/infra"
	"google.golang.org/api/iterator"
)

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
func AddEntry(ctx context.Context, env infra.ToolEnv, content, source string, timestamp *string, imageURL string) (string, error) {
	if env == nil {
		return "", fmt.Errorf("env is required")
	}
	if content == "" {
		return "", fmt.Errorf("content is required and must be a non-empty string")
	}
	if source == "" {
		return "", fmt.Errorf("source is required and must be a string")
	}

	client, err := env.Firestore(ctx)
	if err != nil {
		return "", err
	}

	entryUUID := infra.GenerateUUID()
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
	_, err = client.Collection(KnowledgeCollection).Doc(entryUUID).Set(ctx, doc)
	if err != nil {
		return "", err
	}

	infra.LoggerFrom(ctx).Debug("entry written to Firestore", "uuid", entryUUID, "source", source, "content", content, "image_url", imageURL)
	return entryUUID, nil
}

// UpdateEntryAudio sets the audio_url and transcription fields on an existing entry.
// Call after transcription completes so the entry reflects both the stored audio and its text.
func UpdateEntryAudio(ctx context.Context, env infra.ToolEnv, entryUUID, audioURL, transcription string) error {
	if env == nil {
		return fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return err
	}
	updates := []firestore.Update{
		{Path: "audio_url", Value: audioURL},
		{Path: "transcription", Value: transcription},
		// Replace placeholder content with the actual transcription.
		{Path: "content", Value: transcription},
	}
	_, err = client.Collection(KnowledgeCollection).Doc(entryUUID).Update(ctx, updates)
	return err
}

// GetEntries fetches entries from Firestore, ordered by timestamp descending.
func GetEntries(ctx context.Context, env infra.ToolEnv, limit int) ([]Entry, error) {
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", "log").
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	return infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, err
		}
		e.UUID = doc.Ref.ID
		return e, nil
	})
}

// GetEntriesAsc fetches entries from Firestore, ordered by timestamp ascending (oldest first).
func GetEntriesAsc(ctx context.Context, env infra.ToolEnv, limit int) ([]Entry, error) {
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", "log").
		OrderBy("timestamp", firestore.Asc).
		Limit(limit)
	return infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, err
		}
		e.UUID = doc.Ref.ID
		return e, nil
	})
}

// GetEntriesByDateRange fetches entries within a date range.
func GetEntriesByDateRange(ctx context.Context, env infra.ToolEnv, startDate, endDate string, limit int) ([]Entry, error) {
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", "log").
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	entries, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, err
		}
		e.UUID = doc.Ref.ID
		return e, nil
	})
	if err != nil {
		return nil, infra.WrapFirestoreIndexError(err)
	}
	return entries, nil
}

// SearchEntries searches entries containing keywords (case-insensitive).
func SearchEntries(ctx context.Context, env infra.ToolEnv, keywords string, limit int) ([]Entry, error) {
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	keywordsLower := strings.Fields(strings.ToLower(keywords))
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", "log").
		OrderBy("timestamp", firestore.Desc).
		Limit(500)
	entries, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, err
		}
		contentLower := strings.ToLower(e.Content)
		for _, kw := range keywordsLower {
			if !strings.Contains(contentLower, kw) {
				return Entry{}, fmt.Errorf("skip")
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
func CountEntries(ctx context.Context, env infra.ToolEnv, startDate, endDate *string) (int, error) {
	if env == nil {
		return 0, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
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
		query = client.Collection(KnowledgeCollection).
			Where("node_type", "==", "log").
			Where("timestamp", ">=", start).
			Where("timestamp", "<=", end)
	} else {
		query = client.Collection(KnowledgeCollection).
			Where("node_type", "==", "log")
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
			return 0, infra.WrapFirestoreIndexError(err)
		}
		count++
	}
	return count, nil
}

// GetUniqueSources returns all unique source values from entries.
func GetUniqueSources(ctx context.Context, env infra.ToolEnv) ([]string, error) {
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	iter := client.Collection(KnowledgeCollection).Where("node_type", "==", "log").Limit(1000).Documents(ctx)
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
func GetEntriesBySource(ctx context.Context, env infra.ToolEnv, sourceFilter string, limit int) ([]Entry, error) {
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	sourceFilterLower := strings.ToLower(sourceFilter)
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", "log").
		OrderBy("timestamp", firestore.Desc).
		Limit(500)
	entries, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, err
		}
		if !strings.Contains(strings.ToLower(e.Source), sourceFilterLower) {
			return Entry{}, fmt.Errorf("skip")
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
func GetEntry(ctx context.Context, env infra.ToolEnv, entryUUID string) (*Entry, error) {
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := client.Collection(KnowledgeCollection).Doc(entryUUID).Get(ctx)
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
func GetEntryDates(ctx context.Context, env infra.ToolEnv, entryIDs []string) (map[string]string, error) {
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
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
		e, err := GetEntry(ctx, env, id)
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
func UpdateEntry(ctx context.Context, env infra.ToolEnv, entryUUID, newContent string) error {
	if env == nil {
		return fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(KnowledgeCollection).Doc(entryUUID).Update(ctx, []firestore.Update{
		{Path: "content", Value: newContent},
	})
	return err
}

// DeleteEntry deletes a single entry.
func DeleteEntry(ctx context.Context, env infra.ToolEnv, entryUUID string) error {
	if env == nil {
		return fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(KnowledgeCollection).Doc(entryUUID).Delete(ctx)
	return err
}

// DeleteEntries deletes multiple entries.
func DeleteEntries(ctx context.Context, env infra.ToolEnv, entryUUIDs []string) error {
	if env == nil {
		return fmt.Errorf("env is required")
	}
	if len(entryUUIDs) == 0 {
		return nil
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return err
	}
	batch := client.Batch()
	for _, uuid := range entryUUIDs {
		batch.Delete(client.Collection(KnowledgeCollection).Doc(uuid))
	}
	_, err = batch.Commit(ctx)
	return err
}

// GetDatesWithEntries returns sorted dates (YYYY-MM-DD) that have at least one entry.
func GetDatesWithEntries(ctx context.Context, env infra.ToolEnv) ([]string, error) {
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	iter := client.Collection(KnowledgeCollection).
		Where("node_type", "==", "log").
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
