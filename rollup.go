package jot

import (
	"context"

	"cloud.google.com/go/firestore"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
)

// RunWeeklyRollup synthesizes the last completed week's journal analyses into a weekly_summary knowledge node.
func RunWeeklyRollup(ctx context.Context) (int, error) {
	return agent.RunWeeklyRollup(ctx, jotFOHEnv{})
}

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
		Where("node_type", "==", agent.NodeTypeWeeklySummary).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Asc).
		Limit(limit)
	nodes, err := QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
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
		return nil, WrapFirestoreIndexError(err)
	}
	return nodes, nil
}

// RunMonthlyRollup synthesizes the last completed month's weekly summaries into a monthly_summary knowledge node.
func RunMonthlyRollup(ctx context.Context) (int, error) {
	return agent.RunMonthlyRollup(ctx, jotFOHEnv{})
}
