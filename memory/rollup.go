package memory

import (
	"context"

	"cloud.google.com/go/firestore"
)

// GetWeeklySummaryNodesInRange returns knowledge nodes of type weekly_summary whose timestamp falls in [startDate, endDate] (YYYY-MM-DD).
func (s *Store) GetWeeklySummaryNodesInRange(ctx context.Context, startDate, endDate string, limit int) ([]KnowledgeNode, error) {
	if limit <= 0 {
		limit = 10
	}
	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeWeeklySummary).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Asc).
		Limit(limit)
	nodes, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		data := doc.Data()
		return KnowledgeNode{
			UUID:            doc.Ref.ID,
			Content:         getStringField(data, "content"),
			NodeType:        getStringField(data, "node_type"),
			Metadata:        getStringField(data, "metadata"),
			Timestamp:       getStringField(data, "timestamp"),
			JournalEntryIDs: getStringSliceField(data, "journal_entry_ids"),
		}, nil
	})
	if err != nil {
		return nil, wrapFirestoreIndexError(err)
	}
	return nodes, nil
}
