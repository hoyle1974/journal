// Package memory — semantic vector and keyword search over knowledge nodes.
package memory

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// QuerySimilarNodes performs a KNN vector search in Firestore.
func (s *Store) QuerySimilarNodes(ctx context.Context, queryVector []float32, limit int) ([]KnowledgeNode, error) {
	s.log.Debug("vector search starting", "collection", KnowledgeCollection, "vector_dims", len(queryVector), "limit", limit)

	// Request distance in result so we can log similarity score (1 - cosine distance).
	const distanceResultField = "_vector_distance"
	opts := &firestore.FindNearestOptions{DistanceResultField: distanceResultField}
	vectorQuery := s.db.Collection(KnowledgeCollection).
		FindNearest("embedding", firestore.Vector32(queryVector), limit, firestore.DistanceMeasureCosine, opts)
	iter := vectorQuery.Documents(ctx)
	defer iter.Stop()

	var nodes []KnowledgeNode
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
		// Exclude log entries — this collection now holds both logs and knowledge nodes.
		if getStringField(data, "node_type") == "log" {
			continue
		}
		n := KnowledgeNode{
			UUID:            doc.Ref.ID,
			Content:         getStringField(data, "content"),
			NodeType:        getStringField(data, "node_type"),
			Metadata:        getStringField(data, "metadata"),
			Timestamp:       getStringField(data, "timestamp"),
			JournalEntryIDs: getStringSliceField(data, "journal_entry_ids"),
		}
		nodes = append(nodes, n)
		// Cosine distance: 0 = identical, 2 = opposite. Score = 1 - distance, capped to [0, 1].
		score := 0.0
		if v, ok := data[distanceResultField]; ok {
			var d float64
			switch x := v.(type) {
			case float64:
				d = x
			case float32:
				d = float64(x)
			default:
				d = 0
			}
			score = 1 - d
			if score < 0 {
				score = 0
			}
			if score > 1 {
				score = 1
			}
		}
		scores = append(scores, score)
		s.logFoundNode(n.UUID, score, n.Content)
	}

	s.logRAGQuality(limit, scores)
	return nodes, nil
}

// QuerySimilarSemanticNodes performs a KNN vector search filtered to significance_weight >= minSignificance.
// Requires a composite index: significance_weight ASC + embedding VECTOR on the journal collection.
func (s *Store) QuerySimilarSemanticNodes(ctx context.Context, queryVector []float32, limit int, minSignificance float64) ([]KnowledgeNode, error) {
	const distanceResultField = "_vector_distance"
	opts := &firestore.FindNearestOptions{DistanceResultField: distanceResultField}
	vectorQuery := s.db.Collection(KnowledgeCollection).
		Where("significance_weight", ">=", minSignificance).
		FindNearest("embedding", firestore.Vector32(queryVector), limit, firestore.DistanceMeasureCosine, opts)
	iter := vectorQuery.Documents(ctx)
	defer iter.Stop()

	var nodes []KnowledgeNode
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
		// Exclude log entries even if they somehow pass the significance filter.
		if getStringField(data, "node_type") == "log" {
			continue
		}
		n := KnowledgeNode{
			UUID:            doc.Ref.ID,
			Content:         getStringField(data, "content"),
			NodeType:        getStringField(data, "node_type"),
			Metadata:        getStringField(data, "metadata"),
			Timestamp:       getStringField(data, "timestamp"),
			JournalEntryIDs: getStringSliceField(data, "journal_entry_ids"),
		}
		nodes = append(nodes, n)
		score := 0.0
		if v, ok := data[distanceResultField]; ok {
			var d float64
			switch x := v.(type) {
			case float64:
				d = x
			case float32:
				d = float64(x)
			default:
				d = 0
			}
			score = 1 - d
			if score < 0 {
				score = 0
			}
			if score > 1 {
				score = 1
			}
		}
		scores = append(scores, score)
		s.logFoundNode(n.UUID, score, n.Content)
	}

	s.logRAGQuality(limit, scores)
	return nodes, nil
}

// SearchKnowledgeNodes searches knowledge nodes by keywords (case-insensitive) in Content and Metadata.
func (s *Store) SearchKnowledgeNodes(ctx context.Context, keywords string, limit int) ([]KnowledgeNode, error) {
	keywordsLower := strings.Fields(strings.ToLower(keywords))
	if len(keywordsLower) == 0 {
		return nil, nil
	}
	query := s.db.Collection(KnowledgeCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(500)
	nodes, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		data := doc.Data()
		// Exclude log entries — this collection now holds both logs and knowledge nodes.
		if getStringField(data, "node_type") == "log" {
			return KnowledgeNode{}, errSkipEntry
		}
		content := getStringField(data, "content")
		metadata := getStringField(data, "metadata")
		contentLower := strings.ToLower(content)
		metadataLower := strings.ToLower(metadata)
		for _, kw := range keywordsLower {
			if !strings.Contains(contentLower, kw) && !strings.Contains(metadataLower, kw) {
				return KnowledgeNode{}, errSkipEntry
			}
		}
		return KnowledgeNode{
			UUID:            doc.Ref.ID,
			Content:         content,
			NodeType:        getStringField(data, "node_type"),
			Metadata:        metadata,
			Timestamp:       getStringField(data, "timestamp"),
			JournalEntryIDs: getStringSliceField(data, "journal_entry_ids"),
		}, nil
	})
	if err != nil {
		return nil, err
	}
	if len(nodes) > limit {
		nodes = nodes[:limit]
	}
	return nodes, nil
}

// FindEntityNodeByName does an embedding search for a person/entity by name.
func (s *Store) FindEntityNodeByName(ctx context.Context, entityName string) (*KnowledgeNode, error) {
	query := "Person: " + entityName + " relationship"
	vec, err := s.embedder.GenerateEmbedding(ctx, query, EmbedTaskRetrievalQuery)
	if err != nil {
		return nil, err
	}
	nodes, err := s.QuerySimilarNodes(ctx, vec, 15)
	if err != nil {
		return nil, err
	}
	entityLower := strings.ToLower(strings.TrimSpace(entityName))
	for _, n := range nodes {
		if n.NodeType != "person" {
			continue
		}
		contentLower := strings.ToLower(n.Content)
		if strings.Contains(contentLower, entityLower) || strings.Contains(entityLower, contentLower) {
			return &n, nil
		}
	}
	for i := range nodes {
		if nodes[i].NodeType == "person" {
			return &nodes[i], nil
		}
	}
	return nil, nil
}

// DiscoverRelatedNodes finds nodes semantically related to an entity name that may not be in entity_links.
func (s *Store) DiscoverRelatedNodes(ctx context.Context, entityName string, limit int) ([]KnowledgeNode, error) {
	query := fmt.Sprintf("Facts and information about %s", entityName)
	vec, err := s.embedder.GenerateEmbedding(ctx, query, EmbedTaskRetrievalQuery)
	if err != nil {
		return nil, err
	}
	candidates, err := s.QuerySimilarNodes(ctx, vec, limit*2)
	if err != nil {
		return nil, err
	}
	entityLower := strings.ToLower(strings.TrimSpace(entityName))
	var related []KnowledgeNode
	for _, n := range candidates {
		if n.NodeType == "person" && strings.Contains(strings.ToLower(n.Content), entityLower) {
			continue
		}
		related = append(related, n)
		if len(related) >= limit {
			break
		}
	}
	return related, nil
}
