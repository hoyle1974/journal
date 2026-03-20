package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
)

// QueryLog represents a logged Q&A pair from the FOH loop.
type QueryLog struct {
	UUID      string `firestore:"-" json:"uuid"`
	Question  string `firestore:"question" json:"question"`
	Answer    string `firestore:"answer" json:"answer"`
	Source    string `firestore:"source" json:"source"`
	Timestamp string `firestore:"timestamp" json:"timestamp"`
	IsGap     bool   `firestore:"is_gap" json:"is_gap"`
}

// SaveQuery saves a query and its response to the knowledge collection.
// If isGap is true, the query is recorded as a knowledge gap.
func (s *Store) SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	queryUUID := generateUUID()
	timestamp := time.Now().Format(time.RFC3339)
	if isGap && !strings.Contains(strings.ToLower(answer), "looked for this but found nothing") {
		answer = answer + "\n\n(I looked for this but found nothing.)"
	}
	doc := map[string]interface{}{
		"question":            question,
		"answer":              answer,
		"source":              source,
		"timestamp":           timestamp,
		"is_gap":              isGap,
		"node_type":           NodeTypeQuery,
		"significance_weight": 0.1,
	}
	_, err := s.db.Collection(KnowledgeCollection).Doc(queryUUID).Set(ctx, doc)
	if err != nil {
		return "", fmt.Errorf("save query: %w", err)
	}
	return queryUUID, nil
}

// GetRecentQueries returns the most recent queries.
func (s *Store) GetRecentQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeQuery).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	return queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (QueryLog, error) {
		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			return QueryLog{}, err
		}
		q.UUID = doc.Ref.ID
		return q, nil
	})
}

// SearchQueries searches past queries by keywords.
func (s *Store) SearchQueries(ctx context.Context, keywords string, limit int) ([]QueryLog, error) {
	keywordsLower := strings.Fields(strings.ToLower(keywords))
	fetchLimit := limit * 5
	if fetchLimit < 50 {
		fetchLimit = 50
	}
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeQuery).
		OrderBy("timestamp", firestore.Desc).
		Limit(fetchLimit)
	queries, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (QueryLog, error) {
		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			return QueryLog{}, err
		}
		questionLower := strings.ToLower(q.Question)
		for _, kw := range keywordsLower {
			if !strings.Contains(questionLower, kw) {
				return QueryLog{}, errSkipEntry
			}
		}
		q.UUID = doc.Ref.ID
		return q, nil
	})
	if err != nil {
		return nil, err
	}
	if len(queries) > limit {
		queries = queries[:limit]
	}
	return queries, nil
}

// GetRecentGapQueries returns the most recent queries marked as knowledge gaps.
func (s *Store) GetRecentGapQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeQuery).
		Where("is_gap", "==", true).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	queries, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (QueryLog, error) {
		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			return QueryLog{}, err
		}
		q.UUID = doc.Ref.ID
		return q, nil
	})
	if err != nil {
		return nil, wrapFirestoreIndexError(err)
	}
	return queries, nil
}

// GetQueriesByDateRange returns queries within a date range.
func (s *Store) GetQueriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]QueryLog, error) {
	startDate = padDateStart(startDate)
	endDate = padDateEnd(endDate)
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeQuery).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	queries, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (QueryLog, error) {
		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			return QueryLog{}, err
		}
		q.UUID = doc.Ref.ID
		return q, nil
	})
	if err != nil {
		return nil, wrapFirestoreIndexError(err)
	}
	return queries, nil
}
