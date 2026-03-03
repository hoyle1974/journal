package jot

import (
	"context"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// =============================================================================
// QUERY LOG TYPES
// =============================================================================

// QueryLog represents a logged query.
type QueryLog struct {
	UUID      string `firestore:"-" json:"uuid"`
	Question  string `firestore:"question" json:"question"`
	Answer    string `firestore:"answer" json:"answer"`
	Source    string `firestore:"source" json:"source"`
	Timestamp string `firestore:"timestamp" json:"timestamp"`
	IsGap     bool   `firestore:"is_gap" json:"is_gap"` // true when search tools returned no results (knowledge gap)
}

// =============================================================================
// QUERY LOG OPERATIONS
// =============================================================================

// SaveQuery saves a query and its response. If isGap is true, the query is recorded as a knowledge gap (search returned no results).
func SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return "", err
	}

	queryUUID := GenerateUUID()
	timestamp := time.Now().Format(time.RFC3339)

	doc := map[string]interface{}{
		"question":  question,
		"answer":    answer,
		"source":    source,
		"timestamp": timestamp,
		"is_gap":    isGap,
	}
	if isGap && !strings.Contains(strings.ToLower(answer), "looked for this but found nothing") {
		doc["answer"] = answer + "\n\n(I looked for this but found nothing.)"
	}

	_, err = client.Collection(QueriesCollection).Doc(queryUUID).Set(ctx, doc)
	if err != nil {
		return "", err
	}

	return queryUUID, nil
}

// GetRecentQueries gets the most recent queries.
func GetRecentQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(QueriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var queries []QueryLog
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			continue
		}
		q.UUID = doc.Ref.ID
		queries = append(queries, q)
	}
	return queries, nil
}

// SearchQueries searches past queries by keywords.
func SearchQueries(ctx context.Context, keywords string, limit int) ([]QueryLog, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(QueriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(200).
		Documents(ctx)
	defer iter.Stop()

	keywordsLower := strings.Fields(strings.ToLower(keywords))
	var queries []QueryLog

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			continue
		}

		questionLower := strings.ToLower(q.Question)
		allMatch := true
		for _, kw := range keywordsLower {
			if !strings.Contains(questionLower, kw) {
				allMatch = false
				break
			}
		}

		if allMatch {
			q.UUID = doc.Ref.ID
			queries = append(queries, q)
			if len(queries) >= limit {
				break
			}
		}
	}
	return queries, nil
}

// GetRecentGapQueries returns the most recent queries that were marked as knowledge gaps (no results found).
func GetRecentGapQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(QueriesCollection).
		Where("is_gap", "==", true).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var queries []QueryLog
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, WrapFirestoreIndexError(err)
		}

		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			continue
		}
		q.UUID = doc.Ref.ID
		queries = append(queries, q)
	}
	return queries, nil
}

// GetQueriesByDateRange gets queries within a date range.
func GetQueriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]QueryLog, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}

	iter := client.Collection(QueriesCollection).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var queries []QueryLog
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, WrapFirestoreIndexError(err)
		}

		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			continue
		}
		q.UUID = doc.Ref.ID
		queries = append(queries, q)
	}
	return queries, nil
}
