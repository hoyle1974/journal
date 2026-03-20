package memory

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// EntryWithAnalysis pairs an entry with its parsed journal_analysis (when present).
type EntryWithAnalysis struct {
	Entry    Entry
	Analysis *JournalAnalysis
}

// GetEntriesWithAnalysisByDateRange fetches entries in the date range and parses journal_analysis from each doc.
func (s *Store) GetEntriesWithAnalysisByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]EntryWithAnalysis, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	startDate = padDateStart(startDate)
	endDate = padDateEnd(endDate)
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeLog).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	result, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (EntryWithAnalysis, error) {
		data := doc.Data()
		e := Entry{
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
		return nil, wrapFirestoreIndexError(err)
	}
	return result, nil
}

// QuerySimilarEntries performs a KNN vector search on journal entries.
func (s *Store) QuerySimilarEntries(ctx context.Context, queryVector []float32, limit int) ([]Entry, error) {
	const distanceResultField = "_vector_distance"
	opts := &firestore.FindNearestOptions{DistanceResultField: distanceResultField}
	vectorQuery := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeLog).
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
			s.logVectorSearchFailed(KnowledgeCollection, err, 0)
			return nil, err
		}
		data := doc.Data()
		content := getStringField(data, "content")
		entries = append(entries, Entry{
			UUID:                   doc.Ref.ID,
			Content:                content,
			Source:                 getStringField(data, "source"),
			Timestamp:              getStringField(data, "timestamp"),
			ImageURL:               getStringField(data, "image_url"),
			ParsedImageDescription: getStringField(data, "parsed_image_description"),
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
		s.logFoundEntry(doc.Ref.ID, score, content)
	}
	s.logRAGQuality(limit, scores)
	return entries, nil
}

// BackfillEntryEmbeddings finds entries without embeddings, generates them, and updates docs.
func (s *Store) BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	iter := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeLog).
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
		vector, err := s.embedder.GenerateEmbedding(ctx, content, EmbedTaskRetrievalDocument)
		if err != nil {
			s.log.Warn("backfill embedding failed", "doc", doc.Ref.ID, "error", err)
			continue
		}
		_, err = s.db.Collection(KnowledgeCollection).Doc(doc.Ref.ID).Update(ctx, []firestore.Update{
			{Path: "embedding", Value: firestore.Vector32(vector)},
		})
		if err != nil {
			s.log.Warn("backfill update failed", "doc", doc.Ref.ID, "error", err)
			continue
		}
		processed++
		s.log.Debug("backfill embedded entry", "doc", doc.Ref.ID)
	}
	return processed, nil
}
