// Package memory — SPO edge queries for graph traversal (incoming and outgoing).
package memory

import (
	"context"

	"cloud.google.com/go/firestore"
)

// SPOExtra carries optional Subject-Predicate-Object edge data for relational facts.
type SPOExtra struct {
	Predicate   string
	ObjectValue string
}

// QueryNodesLinkingTo returns nodes whose entity_links array contains targetUUID (incoming edges).
// This finds all nodes that explicitly reference the target as a linked entity.
func (s *Store) QueryNodesLinkingTo(ctx context.Context, targetUUID string, limit int) ([]KnowledgeNode, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("entity_links", "array-contains", targetUUID).
		Limit(limit)
	nodes, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		data := doc.Data()
		n := KnowledgeNode{
			UUID:        doc.Ref.ID,
			Content:     getStringField(data, "content"),
			NodeType:    getStringField(data, "node_type"),
			Metadata:    getStringField(data, "metadata"),
			Timestamp:   getStringField(data, "timestamp"),
			Predicate:   getStringField(data, "predicate"),
			SubjectUUID: getStringField(data, "subject_uuid"),
			ObjectUUID:  getStringField(data, "object_uuid"),
		}
		if v, ok := data["embedding"].(firestore.Vector32); ok {
			n.Embedding = []float32(v)
		}
		return n, nil
	})
	if err != nil {
		return nil, wrapFirestoreIndexError(err)
	}
	return nodes, nil
}

// QueryIncomingSPOEdges returns nodes where object_uuid equals targetUUID (incoming SPO edges).
// This finds all relational nodes where the given entity is the object — i.e. nodes that point to it.
func (s *Store) QueryIncomingSPOEdges(ctx context.Context, targetUUID string, limit int) ([]KnowledgeNode, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("object_uuid", "==", targetUUID).
		Limit(limit)
	nodes, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		data := doc.Data()
		n := KnowledgeNode{
			UUID:        doc.Ref.ID,
			Content:     getStringField(data, "content"),
			NodeType:    getStringField(data, "node_type"),
			Metadata:    getStringField(data, "metadata"),
			Timestamp:   getStringField(data, "timestamp"),
			Predicate:   getStringField(data, "predicate"),
			SubjectUUID: getStringField(data, "subject_uuid"),
			ObjectUUID:  getStringField(data, "object_uuid"),
		}
		if v, ok := data["embedding"].(firestore.Vector32); ok {
			n.Embedding = []float32(v)
		}
		return n, nil
	})
	if err != nil {
		return nil, wrapFirestoreIndexError(err)
	}
	return nodes, nil
}
