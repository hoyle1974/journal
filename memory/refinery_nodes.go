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

// entitySimilarityThreshold is the maximum cosine distance at which two entity names
// are considered semantically equivalent during EnsureNode resolution.
const entitySimilarityThreshold = 0.15

// EnsureNode returns an existing entity node by semantic or deterministic key, or creates one.
// Resolution order:
//  1. Vector search by node_type within entitySimilarityThreshold — catches name variants.
//  2. SHA1 doc ID fast path (exact string match via deterministic key).
//  3. name_key exact match (backfill for pre-existing nodes).
//  4. Create new node if none of the above match.
//
// ts is the source timestamp to anchor the node historically; if empty, time.Now() is used.
func (s *Store) EnsureNode(ctx context.Context, identifier, nodeType, sourceEntryID, ts string) (*KnowledgeNode, error) {
	cleanIdentifier := strings.TrimSpace(identifier)
	if cleanIdentifier == "" {
		return nil, fmt.Errorf("ensure node: empty name")
	}
	if nodeType == "" {
		nodeType = NodeTypePerson
	}

	// Step 1: generate embedding upfront for semantic search and potential creation.
	vector, embErr := s.embedder.GenerateEmbedding(ctx, cleanIdentifier, EmbedTaskRetrievalDocument)
	if embErr != nil {
		return nil, fmt.Errorf("ensure node embedding: %w", embErr)
	}

	// Step 2: semantic search — return an existing node if a near-match exists for this type.
	nearest, err := s.FindNearestByType(ctx, vector, nodeType, entitySimilarityThreshold)
	if err == nil && nearest != nil {
		s.log.Debug("ensure node: semantic match", "input", cleanIdentifier, "matched", nearest.Content, "uuid", nearest.UUID)
		if sourceEntryID != "" {
			updates := buildRelationshipReobserveUpdates(sourceEntryID, ts)
			_, _ = s.db.Collection(KnowledgeCollection).Doc(nearest.UUID).Update(ctx, updates)
		}
		return nearest, nil
	}

	// Step 3: fall through to SHA1 / name_key / create via transaction.
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

		nodeTS := ts
		if nodeTS == "" {
			nodeTS = time.Now().Format(time.RFC3339)
		}
		data := map[string]any{
			"content":             cleanIdentifier,
			"name_key":            strings.ToLower(cleanIdentifier),
			"node_type":           nodeType,
			"timestamp":           nodeTS,
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
		SubjectUUID:   getStringField(data, "subject_uuid"),
		SourceEntryID: getStringField(data, "source_entry_uuid"),
	}
	if v, ok := data["embedding"].(firestore.Vector32); ok {
		out.Embedding = []float32(v)
	}
	return out, nil
}

// stableRelID returns a deterministic document ID for a (subject, predicate, object) triple.
// Using a stable ID ensures that re-observing the same relationship upserts rather than duplicates.
func stableRelID(subjectID, predicate, objectID string) string {
	key := subjectID + ":" + predicate + ":" + objectID
	sum := sha1.Sum([]byte(key))
	return "rel_" + hex.EncodeToString(sum[:])
}

// buildRelationshipReobserveUpdates returns the Firestore Update slice to apply when
// a relationship or entity node is re-observed. It always appends the sourceEntryID to
// journal_entry_ids; if ts is non-empty it also refreshes the node's timestamp so
// that temporal decay scoring reflects the most recent observation date.
func buildRelationshipReobserveUpdates(sourceEntryID, ts string) []firestore.Update {
	updates := []firestore.Update{
		{Path: "journal_entry_ids", Value: firestore.ArrayUnion(sourceEntryID)},
	}
	if ts != "" {
		updates = append(updates, firestore.Update{Path: "timestamp", Value: ts})
	}
	return updates
}

// CreateRelationshipNode creates or updates a reified relationship node with its own embedding.
// The document ID is derived deterministically from (subjectID, predicate, objectID), so
// re-observing the same triple appends to journal_entry_ids rather than creating a duplicate edge.
// ts is the source timestamp to anchor the relationship historically; if empty, time.Now() is used.
func (s *Store) CreateRelationshipNode(ctx context.Context, subjectID, predicate, objectID, sourceEntryID, subjectContent, objectContent, ts string) (string, error) {
	predicate = NormalizedPredicate(predicate)
	if subjectID == "" || objectID == "" || predicate == "" {
		return "", fmt.Errorf("create relationship: subject, predicate, object required")
	}

	relID := stableRelID(subjectID, predicate, objectID)
	ref := s.db.Collection(KnowledgeCollection).Doc(relID)

	// If the edge already exists, append the source entry and refresh the timestamp.
	doc, err := ref.Get(ctx)
	if err == nil && doc.Exists() {
		if sourceEntryID != "" {
			updates := buildRelationshipReobserveUpdates(sourceEntryID, ts)
			_, _ = ref.Update(ctx, updates)
		}
		return relID, nil
	}

	content := relationshipContent(subjectContent, predicate, objectContent, subjectID, objectID)
	vector, embErr := s.embedder.GenerateEmbedding(ctx, content, EmbedTaskRetrievalDocument)
	if embErr != nil {
		return "", fmt.Errorf("create relationship embedding: %w", embErr)
	}

	relTS := ts
	if relTS == "" {
		relTS = time.Now().Format(time.RFC3339)
	}
	data := map[string]any{
		"content":             content,
		"node_type":           NodeTypeRelationship,
		"predicate":           predicate,
		"subject_uuid":        subjectID,
		"object_uuid":         objectID,
		"source_entry_uuid":   sourceEntryID,
		"entity_links":        []string{subjectID, objectID},
		"timestamp":           relTS,
		"domain":              "relationship",
		"significance_weight": 0.8,
		"embedding":           firestore.Vector32(vector),
	}
	if sourceEntryID != "" {
		data["journal_entry_ids"] = []string{sourceEntryID}
	}
	if _, err := ref.Set(ctx, data); err != nil {
		return "", fmt.Errorf("create relationship set: %w", err)
	}
	return relID, nil
}
