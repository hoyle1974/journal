package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/infra"
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
func SaveQuery(ctx context.Context, env infra.ToolEnv, question, answer, source string, isGap bool) (string, error) {
	ctx, span := infra.StartSpan(ctx, "queries.SaveQuery")
	defer span.End()

	if env == nil {
		return "", fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return "", fmt.Errorf("firestore: %w", err)
	}
	queryUUID := infra.GenerateUUID()
	timestamp := time.Now().Format(time.RFC3339)
	if isGap && !strings.Contains(strings.ToLower(answer), "looked for this but found nothing") {
		answer = answer + "\n\n(I looked for this but found nothing.)"
	}
	doc := map[string]interface{}{
		"question":           question,
		"answer":             answer,
		"source":             source,
		"timestamp":          timestamp,
		"is_gap":             isGap,
		"node_type":          NodeTypeQuery,
		"significance_weight": 0.1,
	}
	_, err = client.Collection(KnowledgeCollection).Doc(queryUUID).Set(ctx, doc)
	if err != nil {
		return "", fmt.Errorf("save query: %w", err)
	}
	return queryUUID, nil
}

// GetRecentQueries returns the most recent queries.
func GetRecentQueries(ctx context.Context, env infra.ToolEnv, limit int) ([]QueryLog, error) {
	ctx, span := infra.StartSpan(ctx, "queries.GetRecentQueries")
	defer span.End()

	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore: %w", err)
	}
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeQuery).
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
func SearchQueries(ctx context.Context, env infra.ToolEnv, keywords string, limit int) ([]QueryLog, error) {
	ctx, span := infra.StartSpan(ctx, "queries.SearchQueries")
	defer span.End()

	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore: %w", err)
	}
	keywordsLower := strings.Fields(strings.ToLower(keywords))
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeQuery).
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
func GetRecentGapQueries(ctx context.Context, env infra.ToolEnv, limit int) ([]QueryLog, error) {
	ctx, span := infra.StartSpan(ctx, "queries.GetRecentGapQueries")
	defer span.End()

	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore: %w", err)
	}
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeQuery).
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
func GetQueriesByDateRange(ctx context.Context, env infra.ToolEnv, startDate, endDate string, limit int) ([]QueryLog, error) {
	ctx, span := infra.StartSpan(ctx, "queries.GetQueriesByDateRange")
	defer span.End()

	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore: %w", err)
	}
	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeQuery).
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
