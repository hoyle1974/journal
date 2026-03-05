package jot

import (
	"context"

	"github.com/jackstrohm/jot/pkg/memory"
)

// Re-export knowledge types and constants from pkg/memory.
const KnowledgeCollection = memory.KnowledgeCollection

type KnowledgeNode = memory.KnowledgeNode
type KnowledgeNodeWithLinks = memory.KnowledgeNodeWithLinks

func UpsertKnowledge(ctx context.Context, content, nodeType, metadata string, journalEntryIDs []string) (string, error) {
	return memory.UpsertKnowledge(ctx, content, nodeType, metadata, journalEntryIDs)
}

func UpsertSemanticMemory(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks, journalEntryIDs []string) (string, error) {
	return memory.UpsertSemanticMemory(ctx, content, nodeType, domain, significanceWeight, entityLinks, journalEntryIDs)
}

func FindNearestWithThreshold(ctx context.Context, queryVector []float32, distanceThreshold float64) (*KnowledgeNode, error) {
	return memory.FindNearestWithThreshold(ctx, queryVector, distanceThreshold)
}

func AppendJournalEntryIDsToNode(ctx context.Context, nodeUUID string, entryIDs []string) error {
	return memory.AppendJournalEntryIDsToNode(ctx, nodeUUID, entryIDs)
}

func QuerySimilarNodes(ctx context.Context, queryVector []float32, limit int) ([]KnowledgeNode, error) {
	return memory.QuerySimilarNodes(ctx, queryVector, limit)
}

func SearchKnowledgeNodes(ctx context.Context, keywords string, limit int) ([]KnowledgeNode, error) {
	return memory.SearchKnowledgeNodes(ctx, keywords, limit)
}

func GetKnowledgeNodeByID(ctx context.Context, id string) (*KnowledgeNodeWithLinks, error) {
	return memory.GetKnowledgeNodeByID(ctx, id)
}

func GetKnowledgeNodesByIDs(ctx context.Context, ids []string) ([]KnowledgeNode, error) {
	return memory.GetKnowledgeNodesByIDs(ctx, ids)
}

func FindEntityNodeByName(ctx context.Context, entityName string) (*KnowledgeNode, error) {
	return memory.FindEntityNodeByName(ctx, entityName)
}

func AppendToProjectArchiveSummary(ctx context.Context, projectID, oneLine string) error {
	return memory.AppendToProjectArchiveSummary(ctx, projectID, oneLine)
}

func GetLinkedCompletedProjectID(ctx context.Context, nodeData map[string]interface{}) string {
	return memory.GetLinkedCompletedProjectID(ctx, nodeData)
}

func GetActiveSignals(ctx context.Context, limit int) (string, error) {
	return memory.GetActiveSignals(ctx, limit)
}

func ListKnowledgeNodes(ctx context.Context, limit int) ([]KnowledgeNode, error) {
	return memory.ListKnowledgeNodes(ctx, limit)
}

// RAG fusion: re-export from memory (Entry = journal.Entry).
func FuseKnowledgeNodes(vectorNodes []KnowledgeNode, keywordNodes []KnowledgeNode, topN int) []KnowledgeNode {
	return memory.FuseKnowledgeNodes(vectorNodes, keywordNodes, topN)
}

func FuseEntries(vectorEntries []Entry, keywordEntries []Entry, topN int) []Entry {
	// Entry = journal.Entry so []Entry is assignable to []journal.Entry
	return memory.FuseEntries(vectorEntries, keywordEntries, topN)
}
