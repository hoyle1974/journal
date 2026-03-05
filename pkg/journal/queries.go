package journal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/infra"
)

// QueriesCollection is the Firestore collection name for query logs.
const QueriesCollection = "queries"

// QueryLog represents a logged query.
type QueryLog struct {
	UUID      string `firestore:"-" json:"uuid"`
	Question  string `firestore:"question" json:"question"`
	Answer    string `firestore:"answer" json:"answer"`
	Source    string `firestore:"source" json:"source"`
	Timestamp string `firestore:"timestamp" json:"timestamp"`
	IsGap     bool   `firestore:"is_gap" json:"is_gap"`
}

// SaveQuery saves a query and its response. If isGap is true, the query is recorded as a knowledge gap.
func SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return "", err
	}
	queryUUID := infra.GenerateUUID()
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

// GetRecentQueries returns the most recent queries.
func GetRecentQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	query := client.Collection(QueriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	return infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (QueryLog, error) {
		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			return QueryLog{}, err
		}
		q.UUID = doc.Ref.ID
		return q, nil
	})
}

// SearchQueries searches past queries by keywords.
func SearchQueries(ctx context.Context, keywords string, limit int) ([]QueryLog, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	keywordsLower := strings.Fields(strings.ToLower(keywords))
	query := client.Collection(QueriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(200)
	queries, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (QueryLog, error) {
		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			return QueryLog{}, err
		}
		questionLower := strings.ToLower(q.Question)
		for _, kw := range keywordsLower {
			if !strings.Contains(questionLower, kw) {
				return QueryLog{}, fmt.Errorf("skip")
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
func GetRecentGapQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	query := client.Collection(QueriesCollection).
		Where("is_gap", "==", true).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	queries, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (QueryLog, error) {
		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			return QueryLog{}, err
		}
		q.UUID = doc.Ref.ID
		return q, nil
	})
	if err != nil {
		return nil, infra.WrapFirestoreIndexError(err)
	}
	return queries, nil
}

// GetQueriesByDateRange returns queries within a date range.
func GetQueriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]QueryLog, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}
	query := client.Collection(QueriesCollection).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	queries, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (QueryLog, error) {
		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			return QueryLog{}, err
		}
		q.UUID = doc.Ref.ID
		return q, nil
	})
	if err != nil {
		return nil, infra.WrapFirestoreIndexError(err)
	}
	return queries, nil
}
