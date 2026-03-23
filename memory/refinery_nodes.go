package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
)

func stableEntityDocID(nodeType, name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	sum := sha1.Sum([]byte(nodeType + ":" + key))
	return "entity_" + hex.EncodeToString(sum[:])
}

func looksLikeEntityDocID(identifier string) bool {
	return strings.HasPrefix(strings.TrimSpace(identifier), "entity_")
}

func relationshipContent(subjectContent, predicate, objectContent, subjectID, objectID string) string {
	sub := strings.TrimSpace(subjectContent)
	obj := strings.TrimSpace(objectContent)
	if sub == "" {
		sub = subjectID
	}
	if obj == "" {
		obj = objectID
	}
	return fmt.Sprintf("%s %s %s", sub, predicate, obj)
}

// EnsureNode returns an existing entity node by deterministic key or creates a stub.
// This prevents duplicate stub nodes under concurrent ingest.
func (s *Store) EnsureNode(ctx context.Context, identifier, nodeType, sourceEntryID string) (*KnowledgeNode, error) {
	cleanIdentifier := strings.TrimSpace(identifier)
	if cleanIdentifier == "" {
		return nil, fmt.Errorf("ensure node: empty name")
	}
	if nodeType == "" {
		nodeType = NodeTypePerson
	}

	docID := stableEntityDocID(nodeType, cleanIdentifier)
	if looksLikeEntityDocID(cleanIdentifier) {
		docID = cleanIdentifier
	}
	ref := s.db.Collection(KnowledgeCollection).Doc(docID)

	if err := s.db.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if err == nil && doc.Exists() {
			return nil
		}
		// Backfill path: if a node with this name_key already exists, point to that
		// existing node instead of creating a duplicate stub under a different ID.
		nameKey := strings.ToLower(cleanIdentifier)
		existingByName := s.db.Collection(KnowledgeCollection).Where("name_key", "==", nameKey).Limit(1)
		docs, qErr := tx.Documents(existingByName).GetAll()
		if qErr == nil && len(docs) > 0 && docs[0] != nil && docs[0].Exists() {
			docID = docs[0].Ref.ID
			ref = s.db.Collection(KnowledgeCollection).Doc(docID)
			return nil
		}
		if looksLikeEntityDocID(cleanIdentifier) {
			return fmt.Errorf("ensure node: entity id not found: %s", cleanIdentifier)
		}

		vector, embErr := s.embedder.GenerateEmbedding(ctx, cleanIdentifier, EmbedTaskRetrievalDocument)
		if embErr != nil {
			return fmt.Errorf("ensure node embedding: %w", embErr)
		}
		ts := time.Now().Format(time.RFC3339)
		data := map[string]any{
			"content":             cleanIdentifier,
			"name_key":            strings.ToLower(cleanIdentifier),
			"node_type":           nodeType,
			"metadata":            `{"stub":true}`,
			"timestamp":           ts,
			"domain":              "relationship",
			"significance_weight": 0.55,
			"embedding":           firestore.Vector32(vector),
		}
		if sourceEntryID != "" {
			data["journal_entry_ids"] = []string{sourceEntryID}
		}
		return tx.Set(ref, data, firestore.MergeAll)
	}); err != nil {
		return nil, fmt.Errorf("ensure node txn: %w", err)
	}

	doc, err := ref.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("ensure node readback: %w", err)
	}
	data := doc.Data()
	out := &KnowledgeNode{
		UUID:          doc.Ref.ID,
		Content:       getStringField(data, "content"),
		NodeType:      getStringField(data, "node_type"),
		Metadata:      getStringField(data, "metadata"),
		Timestamp:     getStringField(data, "timestamp"),
		Predicate:     getStringField(data, "predicate"),
		ObjectUUID:    getStringField(data, "object_uuid"),
		SubjectUUID:   getStringField(data, "subject_id"),
		SourceEntryID: getStringField(data, "source_entry_id"),
	}
	if v, ok := data["embedding"].(firestore.Vector32); ok {
		out.Embedding = []float32(v)
	}
	return out, nil
}

// CreateRelationshipNode creates a reified relationship node with its own embedding.
func (s *Store) CreateRelationshipNode(ctx context.Context, subjectID, predicate, objectID, sourceEntryID, subjectContent, objectContent string) (string, error) {
	predicate = NormalizedPredicate(predicate)
	if subjectID == "" || objectID == "" || predicate == "" {
		return "", fmt.Errorf("create relationship: subject, predicate, object required")
	}
	content := relationshipContent(subjectContent, predicate, objectContent, subjectID, objectID)
	vector, err := s.embedder.GenerateEmbedding(ctx, content, EmbedTaskRetrievalDocument)
	if err != nil {
		return "", fmt.Errorf("create relationship embedding: %w", err)
	}
	uuid := generateUUID()
	ts := time.Now().Format(time.RFC3339)
	data := map[string]any{
		"content":             content,
		"node_type":           NodeTypeRelationship,
		"predicate":           predicate,
		"subject_id":          subjectID,
		"object_uuid":         objectID,
		"source_entry_id":     sourceEntryID,
		"entity_links":        []string{subjectID, objectID},
		"timestamp":           ts,
		"domain":              "relationship",
		"significance_weight": 0.8,
		"embedding":           firestore.Vector32(vector),
	}
	if sourceEntryID != "" {
		data["journal_entry_ids"] = []string{sourceEntryID}
	}
	if _, err := s.db.Collection(KnowledgeCollection).Doc(uuid).Set(ctx, data); err != nil {
		return "", fmt.Errorf("create relationship set: %w", err)
	}
	return uuid, nil
}
