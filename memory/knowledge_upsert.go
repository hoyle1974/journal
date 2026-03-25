// Package memory — upsert pipeline with LLM-driven fact-collision detection.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
)

const factCollisionSystemPrompt = `You are a logic engine. Compare New Fact to Existing Fact. If they mean the exact same thing or New Fact is a direct update to Existing Fact, return 'update'. If they contradict each other or refer to different specific details, return 'insert'. If Existing Fact is empty, return 'update'. Reply with ONLY 'update' or 'insert'.`

// evaluateFactCollision decides whether the new fact should overwrite the existing one (update) or be stored as a new node (insert).
func (s *Store) evaluateFactCollision(ctx context.Context, newFact, existingFact string) (string, error) {
	userPrompt := fmt.Sprintf("New Fact:\n%s\n\nExisting Fact:\n%s",
		wrapAsUserData(newFact), wrapAsUserData(existingFact))
	text, err := s.llm.Dispatch(ctx, LLMRequest{
		SystemPrompt: factCollisionSystemPrompt,
		UserPrompt:   userPrompt,
		MaxTokens:    16,
	})
	if err != nil {
		return "", err
	}
	if strings.Contains(strings.ToLower(text), "update") {
		return "update", nil
	}
	return "insert", nil
}

// UpsertKnowledge saves a fact/list item and computes its vector embedding automatically.
func (s *Store) UpsertKnowledge(ctx context.Context, content, nodeType, metadata string, journalEntryIDs []string) (string, error) {
	s.log.Info("upserting knowledge", "content", truncateForLog(content, 50), "node_type", nodeType)

	metaToStore := metadata
	if IsRegistered(nodeType) {
		var m map[string]any
		if metadata != "" {
			_ = json.Unmarshal([]byte(metadata), &m)
		}
		if m == nil {
			m = make(map[string]any)
		}
		if err := ValidateMetadata(nodeType, m); err != nil {
			s.log.Warn("metadata validation failed", "node_type", nodeType, "error", err)
			return "", fmt.Errorf("upsert knowledge: %w", err)
		}
		normalized, err := NormalizeMetadata(nodeType, m)
		if err != nil {
			return "", fmt.Errorf("normalize metadata: %w", err)
		}
		metaToStore, err = MetadataToJSON(normalized)
		if err != nil {
			return "", err
		}
	}

	vector, err := s.embedder.GenerateEmbedding(ctx, content+" "+metaToStore, EmbedTaskRetrievalDocument)
	if err != nil {
		s.log.Error("failed to generate embedding", "error", err)
		return "", err
	}
	s.log.Debug("embedding generated", "dimensions", len(vector))

	timestamp := time.Now().Format(time.RFC3339)
	significanceWeight := 0.5
	domain := "thought"
	if nodeType == NodeTypeUserIdentity {
		significanceWeight = 0.95 // Retain identity statements; high recall priority
		domain = "identity"
	}
	data := map[string]interface{}{
		"content":             content,
		"node_type":           nodeType,
		"metadata":            metaToStore,
		"embedding":           firestore.Vector32(vector),
		"timestamp":           timestamp,
		"significance_weight": significanceWeight,
		"domain":              domain,
	}
	if len(journalEntryIDs) > 0 {
		data["journal_entry_ids"] = journalEntryIDs
	}

	distanceThreshold := 0.25
	vectorQuery := s.db.Collection(KnowledgeCollection).
		FindNearest("embedding", firestore.Vector32(vector), 1, firestore.DistanceMeasureCosine,
			&firestore.FindNearestOptions{DistanceThreshold: &distanceThreshold})

	iter := vectorQuery.Documents(ctx)
	doc, err := iter.Next()
	iter.Stop()

	var nodeUUID string
	// Skip dedup if the nearest doc is a log entry (not a knowledge node).
	isDupCandidate := err == nil && doc != nil && getStringField(doc.Data(), "node_type") != "log"
	if isDupCandidate {
		existingContent := getStringField(doc.Data(), "content")
		action, collErr := s.evaluateFactCollision(ctx, content, existingContent)
		if collErr != nil {
			s.log.Warn("fact collision check failed, inserting new node", "error", collErr)
			action = "insert"
		}
		if action == "update" {
			nodeUUID = doc.Ref.ID
			s.log.Info("updating existing knowledge node", "uuid", nodeUUID)
			_, err = s.db.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
			if err != nil {
				s.log.Error("failed to update knowledge node", "error", err)
				return "", err
			}
		} else {
			nodeUUID = generateUUID()
			_, err = s.db.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
			if err != nil {
				return "", err
			}
			s.log.Info("knowledge node created", "uuid", nodeUUID)
		}
	} else {
		nodeUUID = generateUUID()
		_, err = s.db.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
		if err != nil {
			return "", err
		}
		s.log.Info("knowledge node created", "uuid", nodeUUID)
	}

	return nodeUUID, nil
}

// UpsertSemanticMemory saves a fact with extended schema (significance, domain, etc.).
func (s *Store) UpsertSemanticMemory(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks []string, journalEntryIDs []string) (string, error) {
	metadata := fmt.Sprintf(`{"domain":"%s"}`, domain)
	vector, err := s.embedder.GenerateEmbedding(ctx, content+" "+metadata, EmbedTaskRetrievalDocument)
	if err != nil {
		return "", err
	}
	return s.upsertSemanticMemoryWithVector(ctx, content, nodeType, domain, significanceWeight, entityLinks, journalEntryIDs, vector, nil)
}

// UpsertSemanticMemoryPreembedded is like UpsertSemanticMemory but accepts a precomputed embedding vector,
// skipping the embedding API call. Use this when the vector has already been generated (e.g. dream batch path).
// If vector is nil or empty, falls back to generating a fresh embedding.
func (s *Store) UpsertSemanticMemoryPreembedded(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks []string, journalEntryIDs []string, vector []float32) (string, error) {
	if len(vector) == 0 {
		return s.UpsertSemanticMemory(ctx, content, nodeType, domain, significanceWeight, entityLinks, journalEntryIDs)
	}
	return s.upsertSemanticMemoryWithVector(ctx, content, nodeType, domain, significanceWeight, entityLinks, journalEntryIDs, vector, nil)
}

// UpsertSemanticMemoryPreembeddedWithSPO is like UpsertSemanticMemoryPreembedded but also persists SPO
// predicate and object_value fields when spo is non-nil and spo.Predicate is non-empty.
func (s *Store) UpsertSemanticMemoryPreembeddedWithSPO(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks []string, journalEntryIDs []string, vector []float32, spo *SPOExtra) (string, error) {
	if len(vector) == 0 {
		// Fall back to non-SPO path which will regenerate embedding.
		return s.UpsertSemanticMemory(ctx, content, nodeType, domain, significanceWeight, entityLinks, journalEntryIDs)
	}
	return s.upsertSemanticMemoryWithVector(ctx, content, nodeType, domain, significanceWeight, entityLinks, journalEntryIDs, vector, spo)
}

func (s *Store) upsertSemanticMemoryWithVector(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks []string, journalEntryIDs []string, vector []float32, spo *SPOExtra) (string, error) {
	metadata := fmt.Sprintf(`{"domain":"%s"}`, domain)

	timestamp := time.Now().Format(time.RFC3339)
	distanceThreshold := 0.25
	vectorQuery := s.db.Collection(KnowledgeCollection).
		FindNearest("embedding", firestore.Vector32(vector), 1, firestore.DistanceMeasureCosine,
			&firestore.FindNearestOptions{DistanceThreshold: &distanceThreshold})

	iter := vectorQuery.Documents(ctx)
	doc, err := iter.Next()
	iter.Stop()

	data := map[string]interface{}{
		"content":             content,
		"node_type":           nodeType,
		"metadata":            metadata,
		"embedding":           firestore.Vector32(vector),
		"timestamp":           timestamp,
		"significance_weight": significanceWeight,
		"domain":              domain,
	}
	if len(entityLinks) > 0 {
		data["entity_links"] = entityLinks
	}
	if len(journalEntryIDs) > 0 {
		data["journal_entry_ids"] = journalEntryIDs
	}
	if spo != nil && spo.Predicate != "" {
		data["predicate"] = spo.Predicate
	}

	var nodeUUID string
	// Skip dedup if the nearest doc is a log entry (not a knowledge node).
	isDupCandidate := err == nil && doc != nil && getStringField(doc.Data(), "node_type") != "log"
	if isDupCandidate {
		existingContent := getStringField(doc.Data(), "content")
		action, collErr := s.evaluateFactCollision(ctx, content, existingContent)
		if collErr != nil {
			s.log.Warn("fact collision check failed, inserting new node", "error", collErr)
			action = "insert"
		}
		if action == "update" {
			nodeUUID = doc.Ref.ID
			_, err = s.db.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
		} else {
			nodeUUID = generateUUID()
			_, err = s.db.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
		}
	} else {
		nodeUUID = generateUUID()
		_, err = s.db.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
	}
	if err != nil {
		return "", err
	}
	return nodeUUID, nil
}
