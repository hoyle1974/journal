// Package memory provides knowledge node types and operations (vector-backed long-term memory).
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// KnowledgeCollection is the Firestore collection name for knowledge nodes.
// Points to the unified "journal" collection shared with episodic log entries.
// Knowledge nodes are distinguished from log entries by node_type != "log".
const KnowledgeCollection = "journal"

// EntriesCollection is an alias for KnowledgeCollection for call-site compatibility
// when referring specifically to journal log entries.
const EntriesCollection = KnowledgeCollection

// QueriesCollection is the Firestore collection name for query logs.
// In the unified schema, queries are stored in the same "journal" collection as knowledge nodes.
const QueriesCollection = KnowledgeCollection

// TasksCollection is the Firestore collection name for tasks.
// In the unified schema, tasks are stored in the "journal" collection as knowledge nodes (node_type=task).
const TasksCollection = KnowledgeCollection

// KnowledgeNode represents an arbitrary piece of structured data (Person, Task, Goal, Fact).
type KnowledgeNode struct {
	UUID            string   `firestore:"-" json:"uuid"`
	Content         string   `firestore:"content" json:"content"`
	NodeType        string   `firestore:"node_type" json:"node_type"`
	Metadata        string   `firestore:"metadata" json:"metadata"`
	Timestamp       string   `firestore:"timestamp" json:"timestamp"`
	JournalEntryIDs []string `firestore:"-" json:"journal_entry_ids,omitempty"`
	// SPO triple fields. Predicate is non-empty only for relational nodes extracted in
	// Subject | Predicate | Object format (e.g. "prefers", "works_at", "is_part_of").
	// ObjectUUID is the UUID of the object entity node when it corresponds to an existing
	// knowledge node; empty when the object is a raw string with no node.
	Predicate  string `firestore:"predicate,omitempty" json:"predicate,omitempty"`
	ObjectUUID string `firestore:"object_uuid,omitempty" json:"object_uuid,omitempty"`
}

// KnowledgeNodeWithLinks extends KnowledgeNode with entity_links and journal_entry_ids for graph traversal.
type KnowledgeNodeWithLinks struct {
	KnowledgeNode
	EntityLinks     []string
	JournalEntryIDs []string
}

func truncateForLog(s string, maxLen int) string {
	if len([]rune(s)) <= maxLen {
		return s
	}
	return truncateString(s, maxLen) + "..."
}

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
			return "", fmt.Errorf("invalid metadata for node_type %q: %w", nodeType, err)
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
		"last_recalled_at":    timestamp,
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

// SPOExtra carries optional Subject-Predicate-Object edge data for relational facts.
type SPOExtra struct {
	Predicate   string
	ObjectValue string
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
	now := timestamp
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
		"last_recalled_at":    now,
	}
	if len(entityLinks) > 0 {
		data["entity_links"] = entityLinks
	}
	if len(journalEntryIDs) > 0 {
		data["journal_entry_ids"] = journalEntryIDs
	}
	if spo != nil && spo.Predicate != "" {
		data["predicate"] = spo.Predicate
		data["object_value"] = spo.ObjectValue
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

// FindNearestWithThreshold returns the single nearest knowledge node if within distanceThreshold, else nil.
func (s *Store) FindNearestWithThreshold(ctx context.Context, queryVector []float32, distanceThreshold float64) (*KnowledgeNode, error) {
	vectorQuery := s.db.Collection(KnowledgeCollection).
		FindNearest("embedding", firestore.Vector32(queryVector), 1, firestore.DistanceMeasureCosine,
			&firestore.FindNearestOptions{DistanceThreshold: &distanceThreshold})
	iter := vectorQuery.Documents(ctx)
	doc, err := iter.Next()
	iter.Stop()
	if err != nil || doc == nil {
		return nil, nil
	}
	data := doc.Data()
	n := &KnowledgeNode{
		UUID:            doc.Ref.ID,
		Content:         getStringField(data, "content"),
		NodeType:        getStringField(data, "node_type"),
		Metadata:        getStringField(data, "metadata"),
		Timestamp:       getStringField(data, "timestamp"),
		JournalEntryIDs: getStringSliceField(data, "journal_entry_ids"),
	}
	return n, nil
}

// AppendJournalEntryIDsToNode merges entryIDs into the node's journal_entry_ids (deduped) and updates the document.
func (s *Store) AppendJournalEntryIDsToNode(ctx context.Context, nodeUUID string, entryIDs []string) error {
	if len(entryIDs) == 0 {
		return nil
	}
	doc, err := s.db.Collection(KnowledgeCollection).Doc(nodeUUID).Get(ctx)
	if err != nil {
		return err
	}
	existing := getStringSliceField(doc.Data(), "journal_entry_ids")
	seen := make(map[string]bool)
	for _, id := range existing {
		seen[id] = true
	}
	for _, id := range entryIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			existing = append(existing, id)
		}
	}
	_, err = s.db.Collection(KnowledgeCollection).Doc(nodeUUID).Update(ctx, []firestore.Update{
		{Path: "journal_entry_ids", Value: existing},
	})
	return err
}

// AddEntityLink appends a target UUID (e.g. a fact or project node) to a source node's entity_links.
// Idempotent: if targetUUID is already in the list, no update is performed.
func (s *Store) AddEntityLink(ctx context.Context, sourceUUID, targetUUID string) error {
	if sourceUUID == "" || targetUUID == "" {
		return nil
	}
	return s.db.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ref := s.db.Collection(KnowledgeCollection).Doc(sourceUUID)
		doc, err := tx.Get(ref)
		if err != nil {
			return err
		}
		links := getStringSliceField(doc.Data(), "entity_links")
		for _, l := range links {
			if l == targetUUID {
				return nil
			}
		}
		links = append(links, targetUUID)
		return tx.Update(ref, []firestore.Update{
			{Path: "entity_links", Value: links},
		})
	})
}

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

// GetKnowledgeNodeByID loads one document from KnowledgeCollection and returns it with entity_links and journal_entry_ids.
func (s *Store) GetKnowledgeNodeByID(ctx context.Context, id string) (*KnowledgeNodeWithLinks, error) {
	doc, err := s.db.Collection(KnowledgeCollection).Doc(id).Get(ctx)
	if err != nil {
		return nil, err
	}
	data := doc.Data()
	n := &KnowledgeNodeWithLinks{
		KnowledgeNode: KnowledgeNode{
			UUID:      doc.Ref.ID,
			Content:   getStringField(data, "content"),
			NodeType:  getStringField(data, "node_type"),
			Metadata:  getStringField(data, "metadata"),
			Timestamp: getStringField(data, "timestamp"),
		},
		EntityLinks:     getStringSliceField(data, "entity_links"),
		JournalEntryIDs: getStringSliceField(data, "journal_entry_ids"),
	}
	return n, nil
}

// GetKnowledgeNodesByIDs fetches multiple knowledge nodes by UUID.
func (s *Store) GetKnowledgeNodesByIDs(ctx context.Context, ids []string) ([]KnowledgeNode, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	deduped := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			deduped = append(deduped, id)
		}
	}
	var nodes []KnowledgeNode
	for _, id := range deduped {
		doc, err := s.db.Collection(KnowledgeCollection).Doc(id).Get(ctx)
		if err != nil {
			s.log.Debug("get knowledge node by id skip", "id", id, "error", err)
			continue
		}
		data := doc.Data()
		n := KnowledgeNode{
			UUID:            doc.Ref.ID,
			Content:         getStringField(data, "content"),
			NodeType:        getStringField(data, "node_type"),
			Metadata:        getStringField(data, "metadata"),
			Timestamp:       getStringField(data, "timestamp"),
			JournalEntryIDs: getStringSliceField(data, "journal_entry_ids"),
		}
		nodes = append(nodes, n)
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

// FindProjectOrGoalByName finds the nearest project or goal knowledge node by semantic similarity to the given name.
func (s *Store) FindProjectOrGoalByName(ctx context.Context, projectName string) (*KnowledgeNode, error) {
	vec, err := s.embedder.GenerateEmbedding(ctx, "Project: "+projectName, EmbedTaskRetrievalQuery)
	if err != nil {
		return nil, err
	}
	nodes, err := s.QuerySimilarNodes(ctx, vec, 5)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		if nodes[i].NodeType == NodeTypeProject || nodes[i].NodeType == NodeTypeGoal {
			return &nodes[i], nil
		}
	}
	return nil, nil
}

// UpdateProjectStatus sets the status field on a project or goal node's metadata and updates last_recalled_at.
// The node must exist and have node_type "project" or "goal"; status is validated against the project/goal schema.
func (s *Store) UpdateProjectStatus(ctx context.Context, nodeID, status string) error {
	node, err := s.GetKnowledgeNodeByID(ctx, nodeID)
	if err != nil {
		return err
	}
	if node == nil {
		return fmt.Errorf("knowledge node %q not found", nodeID)
	}
	if node.NodeType != NodeTypeProject && node.NodeType != NodeTypeGoal {
		return fmt.Errorf("node %q is not a project or goal (node_type=%q)", nodeID, node.NodeType)
	}

	var meta map[string]any
	if node.Metadata != "" {
		_ = json.Unmarshal([]byte(node.Metadata), &meta)
	}
	if meta == nil {
		meta = make(map[string]any)
	}
	meta["status"] = strings.ToLower(strings.TrimSpace(status))
	if err := ValidateMetadata(node.NodeType, meta); err != nil {
		return fmt.Errorf("invalid status: %w", err)
	}
	normalized, err := NormalizeMetadata(node.NodeType, meta)
	if err != nil {
		return fmt.Errorf("normalize metadata: %w", err)
	}
	metaJSON, err := MetadataToJSON(normalized)
	if err != nil {
		return err
	}

	_, err = s.db.Collection(KnowledgeCollection).Doc(nodeID).Update(ctx, []firestore.Update{
		{Path: "metadata", Value: metaJSON},
		{Path: "last_recalled_at", Value: time.Now().Format(time.RFC3339)},
	})
	return err
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

func metadataStatus(metadata string) string {
	if metadata == "" {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(metadata), &m); err != nil {
		return ""
	}
	s, _ := m["status"].(string)
	return s
}

// AppendToProjectArchiveSummary appends a one-line summary to a project/goal node's archive_summary in metadata.
func (s *Store) AppendToProjectArchiveSummary(ctx context.Context, projectID, oneLine string) error {
	doc, err := s.db.Collection(KnowledgeCollection).Doc(projectID).Get(ctx)
	if err != nil {
		return err
	}
	data := doc.Data()
	metadataStr := getStringField(data, "metadata")
	var meta map[string]interface{}
	if metadataStr != "" {
		_ = json.Unmarshal([]byte(metadataStr), &meta)
	}
	if meta == nil {
		meta = make(map[string]interface{})
	}
	current, _ := meta["archive_summary"].(string)
	if current == "" {
		current = getStringField(data, "archive_summary")
	}
	line := oneLine
	if len(line) > 200 {
		line = truncateToMaxBytes(line, 197) + "..."
	}
	if current != "" {
		current += "\n- " + line
	} else {
		current = "- " + line
	}
	meta["archive_summary"] = current
	updatedMetadata, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	_, err = s.db.Collection(KnowledgeCollection).Doc(projectID).Update(ctx, []firestore.Update{
		{Path: "metadata", Value: string(updatedMetadata)},
	})
	return err
}

// GetLinkedCompletedProjectID returns the ID of a completed project linked to this node, or "".
func (s *Store) GetLinkedCompletedProjectID(ctx context.Context, nodeData map[string]interface{}) string {
	metadataStr := getStringField(nodeData, "metadata")
	var meta map[string]interface{}
	if metadataStr != "" {
		_ = json.Unmarshal([]byte(metadataStr), &meta)
	}
	if meta != nil {
		if pid, ok := meta["parent_goal"].(string); ok && pid != "" {
			if s.isCompletedProjectByID(ctx, pid) {
				return pid
			}
		}
		if pid, ok := meta["project_id"].(string); ok && pid != "" {
			if s.isCompletedProjectByID(ctx, pid) {
				return pid
			}
		}
	}
	for _, id := range getStringSliceField(nodeData, "entity_links") {
		if s.isCompletedProjectByID(ctx, id) {
			return id
		}
	}
	return ""
}

func (s *Store) isCompletedProjectByID(ctx context.Context, id string) bool {
	node, err := s.GetKnowledgeNodeByID(ctx, id)
	if err != nil || node == nil {
		return false
	}
	return (node.NodeType == "project" || node.NodeType == "goal") && metadataStatus(node.Metadata) == "completed"
}

// GetUserIdentityNodes returns knowledge nodes of type user_identity, for easy retrieval of self-referential identity statements.
func (s *Store) GetUserIdentityNodes(ctx context.Context, limit int) ([]KnowledgeNode, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeUserIdentity).
		OrderBy("timestamp", firestore.Desc).
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

// GetActiveSignals retrieves recent proactive signals (selfmodel thought nodes) for the FOH.
func (s *Store) GetActiveSignals(ctx context.Context, limit int) (string, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("domain", "==", "selfmodel").
		Where("node_type", "==", "thought").
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	signals, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (string, error) {
		data := doc.Data()
		content := getStringField(data, "content")
		if content == "" {
			return "", errSkipEntry
		}
		ts := getStringField(data, "timestamp")
		if len([]rune(ts)) > 19 {
			ts = truncateString(ts, 19)
		}
		if ts == "" {
			ts = "(no date)"
		}
		return fmt.Sprintf("- [%s] %s", ts, content), nil
	})
	if err != nil {
		return "", wrapFirestoreIndexError(err)
	}
	if len(signals) == 0 {
		return "", nil
	}
	return strings.Join(signals, "\n"), nil
}

// QueryNodesLinkingTo returns nodes whose entity_links array contains targetUUID (incoming edges).
// This finds all nodes that explicitly reference the target as a linked entity.
func (s *Store) QueryNodesLinkingTo(ctx context.Context, targetUUID string, limit int) ([]KnowledgeNode, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("entity_links", "array-contains", targetUUID).
		Limit(limit)
	nodes, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		data := doc.Data()
		return KnowledgeNode{
			UUID:       doc.Ref.ID,
			Content:    getStringField(data, "content"),
			NodeType:   getStringField(data, "node_type"),
			Metadata:   getStringField(data, "metadata"),
			Timestamp:  getStringField(data, "timestamp"),
			Predicate:  getStringField(data, "predicate"),
			ObjectUUID: getStringField(data, "object_uuid"),
		}, nil
	})
	if err != nil {
		return nil, wrapFirestoreIndexError(err)
	}
	return nodes, nil
}

// QueryOutgoingEdges returns nodes where object_uuid equals subjectUUID (outgoing SPO edges).
// This finds all relational nodes where the given entity is the subject.
func (s *Store) QueryOutgoingEdges(ctx context.Context, subjectUUID string, limit int) ([]KnowledgeNode, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("object_uuid", "==", subjectUUID).
		Limit(limit)
	nodes, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		data := doc.Data()
		return KnowledgeNode{
			UUID:       doc.Ref.ID,
			Content:    getStringField(data, "content"),
			NodeType:   getStringField(data, "node_type"),
			Metadata:   getStringField(data, "metadata"),
			Timestamp:  getStringField(data, "timestamp"),
			Predicate:  getStringField(data, "predicate"),
			ObjectUUID: getStringField(data, "object_uuid"),
		}, nil
	})
	if err != nil {
		return nil, wrapFirestoreIndexError(err)
	}
	return nodes, nil
}

// ListKnowledgeNodes lists all knowledge nodes (for diagnostics).
func (s *Store) ListKnowledgeNodes(ctx context.Context, limit int) ([]KnowledgeNode, error) {
	s.log.Info("listing knowledge nodes", "collection", KnowledgeCollection, "limit", limit)
	iter := s.db.Collection(KnowledgeCollection).Limit(limit).Documents(ctx)
	defer iter.Stop()

	var nodes []KnowledgeNode
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		data := doc.Data()
		// Exclude log entries from knowledge node listings.
		if getStringField(data, "node_type") == "log" {
			continue
		}
		var n KnowledgeNode
		if err := doc.DataTo(&n); err != nil {
			if content, ok := data["content"].(string); ok {
				n.Content = content
			}
			if nodeType, ok := data["node_type"].(string); ok {
				n.NodeType = nodeType
			}
			if metadata, ok := data["metadata"].(string); ok {
				n.Metadata = metadata
			}
			if ts, ok := data["timestamp"].(string); ok {
				n.Timestamp = ts
			}
		}
		n.UUID = doc.Ref.ID
		nodes = append(nodes, n)
	}
	return nodes, nil
}
