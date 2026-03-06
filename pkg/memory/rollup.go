package memory

import (
	"context"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/infra"
)

// GetWeeklySummaryNodesInRange returns knowledge nodes of type weekly_summary whose timestamp falls in [startDate, endDate] (YYYY-MM-DD).
func GetWeeklySummaryNodesInRange(ctx context.Context, startDate, endDate string, limit int) ([]KnowledgeNode, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeWeeklySummary).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Asc).
		Limit(limit)
	nodes, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		data := doc.Data()
		return KnowledgeNode{
			UUID:            doc.Ref.ID,
			Content:         infra.GetStringField(data, "content"),
			NodeType:        infra.GetStringField(data, "node_type"),
			Metadata:        infra.GetStringField(data, "metadata"),
			Timestamp:       infra.GetStringField(data, "timestamp"),
			JournalEntryIDs: infra.GetStringSliceField(data, "journal_entry_ids"),
		}, nil
	})
	if err != nil {
		return nil, infra.WrapFirestoreIndexError(err)
	}
	return nodes, nil
}
