package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/memory"
	"google.golang.org/api/iterator"
)

// =============================================================================
// KNOWLEDGE NODE TYPES (Vector-backed Long-Term Memory)
// =============================================================================

const KnowledgeCollection = "knowledge_nodes"

// KnowledgeNode represents an arbitrary piece of structured data (Person, Task, Goal, Fact).
// JournalEntryIDs is populated when reading from Firestore (e.g. QuerySimilarNodes, SearchKnowledgeNodes) for source receipt linkage.
type KnowledgeNode struct {
	UUID            string   `firestore:"-" json:"uuid"`
	Content         string   `firestore:"content" json:"content"`
	NodeType        string   `firestore:"node_type" json:"node_type"` // e.g., "list", "person", "fact"
	Metadata        string   `firestore:"metadata" json:"metadata"`   // JSON string of relationships/attributes
	Timestamp       string   `firestore:"timestamp" json:"timestamp"`
	JournalEntryIDs []string `firestore:"-" json:"journal_entry_ids,omitempty"` // source entry UUIDs; set when loading from DB
	// Note: embedding field is excluded from struct as it causes decoding issues with Firestore's vector type
}

// KnowledgeNodeWithLinks extends KnowledgeNode with entity_links and journal_entry_ids for graph traversal.
type KnowledgeNodeWithLinks struct {
	KnowledgeNode
	EntityLinks     []string // UUIDs of related knowledge nodes
	JournalEntryIDs  []string // UUIDs of source journal entries
}

// =============================================================================
// KNOWLEDGE NODE OPERATIONS
// =============================================================================

// truncateForLog truncates a string for safe logging.
func truncateForLog(s string, maxLen int) string {
	if len([]rune(s)) <= maxLen {
		return s
	}
	return SafeTruncate(s, maxLen) + "..."
}

// UpsertKnowledge saves a fact/list item and computes its vector embedding automatically.
// For registered node types (person, project, goal, etc.), metadata is validated and normalized
// before storage. Use node_type "generic" when the LLM cannot confidently categorize a fact.
func UpsertKnowledge(ctx context.Context, content, nodeType, metadata string) (string, error) {
	ctx, span := StartSpan(ctx, "knowledge.upsert")
	defer span.End()

	LoggerFrom(ctx).Info("upserting knowledge", "content", truncateForLog(content, 50), "node_type", nodeType)

	// Parse, validate, and normalize metadata for registered node types
	metaToStore := metadata
	if memory.IsRegistered(nodeType) {
		var m map[string]any
		if metadata != "" {
			_ = json.Unmarshal([]byte(metadata), &m)
		}
		if m == nil {
			m = make(map[string]any)
		}
		if err := memory.ValidateMetadata(nodeType, m); err != nil {
			LoggerFrom(ctx).Warn("metadata validation failed", "node_type", nodeType, "error", err)
			span.RecordError(err)
			return "", fmt.Errorf("invalid metadata for node_type %q: %w", nodeType, err)
		}
		normalized, err := memory.NormalizeMetadata(nodeType, m)
		if err != nil {
			span.RecordError(err)
			return "", fmt.Errorf("normalize metadata: %w", err)
		}
		metaToStore, err = memory.MetadataToJSON(normalized)
		if err != nil {
			span.RecordError(err)
			return "", err
		}
	}

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		LoggerFrom(ctx).Error("failed to get firestore client", "error", err)
		span.RecordError(err)
		return "", err
	}

	// 1. Generate the vector embedding (RETRIEVAL_DOCUMENT for stored documents)
	vector, err := GenerateEmbedding(ctx, content+" "+metaToStore, EmbedTaskRetrievalDocument)
	if err != nil {
		LoggerFrom(ctx).Error("failed to generate embedding", "error", err)
		span.RecordError(err)
		return "", err
	}
	LoggerFrom(ctx).Debug("embedding generated", "dimensions", len(vector))

	timestamp := time.Now().Format(time.RFC3339)

	// 2. Check if very similar knowledge already exists (true upsert)
	// Use FindNearest with DistanceThreshold to find only very close matches
	distanceThreshold := 0.15 // Cosine distance < 0.15 means very similar
	vectorQuery := client.Collection(KnowledgeCollection).
		FindNearest("embedding", firestore.Vector32(vector), 1, firestore.DistanceMeasureCosine,
			&firestore.FindNearestOptions{DistanceThreshold: &distanceThreshold})

	iter := vectorQuery.Documents(ctx)
	doc, err := iter.Next()
	iter.Stop()

	var nodeUUID string
	if err == nil && doc != nil {
		existingContent := getStringField(doc.Data(), "content")
		action, collErr := EvaluateFactCollision(ctx, content, existingContent)
		if collErr != nil {
			LoggerFrom(ctx).Warn("fact collision check failed, inserting new node", "error", collErr)
			action = "insert"
		}
		if action == "update" {
			nodeUUID = doc.Ref.ID
			LoggerFrom(ctx).Info("updating existing knowledge node", "uuid", nodeUUID)
			_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, map[string]interface{}{
				"content":             content,
				"node_type":           nodeType,
				"metadata":            metaToStore,
				"embedding":           firestore.Vector32(vector),
				"timestamp":           timestamp,
				"significance_weight": 0.5,
				"domain":              "thought",
				"last_recalled_at":    timestamp,
			})
			if err != nil {
				LoggerFrom(ctx).Error("failed to update knowledge node", "error", err)
				span.RecordError(err)
				return "", err
			}
			LoggerFrom(ctx).Info("knowledge node updated", "uuid", nodeUUID)
		} else {
			nodeUUID = GenerateUUID()
			_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, map[string]interface{}{
				"content":             content,
				"node_type":           nodeType,
				"metadata":            metaToStore,
				"embedding":           firestore.Vector32(vector),
				"timestamp":           timestamp,
				"significance_weight": 0.5,
				"domain":              "thought",
				"last_recalled_at":    timestamp,
			})
			if err != nil {
				LoggerFrom(ctx).Error("failed to save knowledge node", "error", err)
				span.RecordError(err)
				return "", err
			}
			LoggerFrom(ctx).Info("knowledge node created", "uuid", nodeUUID)
		}
	} else {
		// No similar node found - create new
		nodeUUID = GenerateUUID()
		_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, map[string]interface{}{
			"content":             content,
			"node_type":           nodeType,
			"metadata":            metaToStore,
			"embedding":           firestore.Vector32(vector),
			"timestamp":           timestamp,
			"significance_weight": 0.5,
			"domain":              "thought",
			"last_recalled_at":    timestamp,
		})
		if err != nil {
			LoggerFrom(ctx).Error("failed to save knowledge node", "error", err)
			span.RecordError(err)
			return "", err
		}
		LoggerFrom(ctx).Info("knowledge node created", "uuid", nodeUUID)
	}

	span.SetAttributes(map[string]string{
		"node_uuid": nodeUUID,
		"node_type": nodeType,
	})

	return nodeUUID, nil
}

// UpsertSemanticMemory saves a fact with extended schema (significance, domain, etc.).
func UpsertSemanticMemory(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks []string, journalEntryIDs []string) (string, error) {
	ctx, span := StartSpan(ctx, "semantic.upsert")
	defer span.End()

	metadata := fmt.Sprintf(`{"domain":"%s"}`, domain)

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return "", err
	}

	vector, err := GenerateEmbedding(ctx, content+" "+metadata, EmbedTaskRetrievalDocument)
	if err != nil {
		return "", err
	}

	timestamp := time.Now().Format(time.RFC3339)
	now := timestamp

	distanceThreshold := 0.15
	vectorQuery := client.Collection(KnowledgeCollection).
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

	var nodeUUID string
	if err == nil && doc != nil {
		existingContent := getStringField(doc.Data(), "content")
		action, collErr := EvaluateFactCollision(ctx, content, existingContent)
		if collErr != nil {
			LoggerFrom(ctx).Warn("fact collision check failed, inserting new node", "error", collErr)
			action = "insert"
		}
		if action == "update" {
			nodeUUID = doc.Ref.ID
			_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
		} else {
			nodeUUID = GenerateUUID()
			_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
		}
	} else {
		nodeUUID = GenerateUUID()
		_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
	}
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	return nodeUUID, nil
}

// QuerySimilarNodes performs a KNN vector search in Firestore.
func QuerySimilarNodes(ctx context.Context, queryVector []float32, limit int) ([]KnowledgeNode, error) {
	ctx, span := StartSpan(ctx, "knowledge.query_similar")
	defer span.End()

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	LoggerFrom(ctx).Debug("vector search starting",
		"collection", KnowledgeCollection,
		"vector_dims", len(queryVector),
		"limit", limit,
	)

	// Use Firestore's native FindNearest vector search
	vectorQuery := client.Collection(KnowledgeCollection).
		FindNearest("embedding", firestore.Vector32(queryVector), limit, firestore.DistanceMeasureCosine, nil)

	iter := vectorQuery.Documents(ctx)
	defer iter.Stop()

	var nodes []KnowledgeNode
	docCount := 0
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			LoggerFrom(ctx).Error("vector search iteration error", "error", err)
			span.RecordError(err)
			return nil, err
		}
		docCount++

		// Extract fields manually to avoid issues with vector field decoding
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
		LoggerFrom(ctx).Debug("found node", "uuid", n.UUID, "content", truncateForLog(n.Content, 50))
	}

	LoggerFrom(ctx).Debug("vector search complete", "docs_scanned", docCount, "nodes_returned", len(nodes))

	span.SetAttributes(map[string]string{
		"results_count": fmt.Sprintf("%d", len(nodes)),
	})

	return nodes, nil
}

// SearchKnowledgeNodes searches knowledge nodes by keywords (case-insensitive) in Content and Metadata.
// Fetches up to 500 most recently updated documents from KnowledgeCollection, then filters in-memory.
func SearchKnowledgeNodes(ctx context.Context, keywords string, limit int) ([]KnowledgeNode, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	keywordsLower := strings.Fields(strings.ToLower(keywords))
	if len(keywordsLower) == 0 {
		return nil, nil
	}

	query := client.Collection(KnowledgeCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(500)
	nodes, err := QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		data := doc.Data()
		content := getStringField(data, "content")
		metadata := getStringField(data, "metadata")
		contentLower := strings.ToLower(content)
		metadataLower := strings.ToLower(metadata)
		for _, kw := range keywordsLower {
			if !strings.Contains(contentLower, kw) && !strings.Contains(metadataLower, kw) {
				return KnowledgeNode{}, fmt.Errorf("skip")
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
func GetKnowledgeNodeByID(ctx context.Context, id string) (*KnowledgeNodeWithLinks, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := client.Collection(KnowledgeCollection).Doc(id).Get(ctx)
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

// GetKnowledgeNodesByIDs fetches multiple knowledge nodes by UUID. Missing IDs are skipped. Order is not guaranteed.
func GetKnowledgeNodesByIDs(ctx context.Context, ids []string) ([]KnowledgeNode, error) {
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
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	var nodes []KnowledgeNode
	for _, id := range deduped {
		doc, err := client.Collection(KnowledgeCollection).Doc(id).Get(ctx)
		if err != nil {
			LoggerFrom(ctx).Debug("get knowledge node by id skip", "id", id, "error", err)
			continue
		}
		data := doc.Data()
		n := KnowledgeNode{
			UUID:      doc.Ref.ID,
			Content:   getStringField(data, "content"),
			NodeType:  getStringField(data, "node_type"),
			Metadata:  getStringField(data, "metadata"),
			Timestamp: getStringField(data, "timestamp"),
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// FindEntityNodeByName does an embedding search for a person/entity by name and returns the best-matching person node, if any.
func FindEntityNodeByName(ctx context.Context, entityName string) (*KnowledgeNode, error) {
	query := "Person: " + entityName + " relationship"
	vec, err := GenerateEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}
	nodes, err := QuerySimilarNodes(ctx, vec, 15)
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
	// If no content match, return first person node from semantic search
	for i := range nodes {
		if nodes[i].NodeType == "person" {
			return &nodes[i], nil
		}
	}
	return nil, nil
}

// metadataStatus returns the "status" value from a metadata JSON string (e.g. "completed").
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

// AppendToProjectArchiveSummary appends a one-line summary to a project/goal node's archive_summary in metadata (create if missing).
func AppendToProjectArchiveSummary(ctx context.Context, projectID, oneLine string) error {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	doc, err := client.Collection(KnowledgeCollection).Doc(projectID).Get(ctx)
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
		// Backward compat: read from top-level field if migration not yet run
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
	_, err = client.Collection(KnowledgeCollection).Doc(projectID).Update(ctx, []firestore.Update{
		{Path: "metadata", Value: string(updatedMetadata)},
	})
	return err
}

// GetLinkedCompletedProjectID returns the ID of a completed project linked to this node (via metadata parent_goal/project_id or entity_links), or "".
func GetLinkedCompletedProjectID(ctx context.Context, nodeData map[string]interface{}) string {
	metadataStr := getStringField(nodeData, "metadata")
	var meta map[string]interface{}
	if metadataStr != "" {
		_ = json.Unmarshal([]byte(metadataStr), &meta)
	}
	if meta != nil {
		if pid, ok := meta["parent_goal"].(string); ok && pid != "" {
			if isCompletedProjectByID(ctx, pid) {
				return pid
			}
		}
		if pid, ok := meta["project_id"].(string); ok && pid != "" {
			if isCompletedProjectByID(ctx, pid) {
				return pid
			}
		}
	}
	for _, id := range getStringSliceField(nodeData, "entity_links") {
		if isCompletedProjectByID(ctx, id) {
			return id
		}
	}
	return ""
}

func isCompletedProjectByID(ctx context.Context, id string) bool {
	node, err := GetKnowledgeNodeByID(ctx, id)
	if err != nil || node == nil {
		return false
	}
	return (node.NodeType == "project" || node.NodeType == "goal") && metadataStatus(node.Metadata) == "completed"
}

// GetActiveSignals retrieves recent proactive signals (selfmodel thought nodes) for the FOH.
func GetActiveSignals(ctx context.Context, limit int) (string, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return "", err
	}
	query := client.Collection(KnowledgeCollection).
		Where("domain", "==", "selfmodel").
		Where("node_type", "==", "thought").
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	signals, err := QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (string, error) {
		data := doc.Data()
		content := getStringField(data, "content")
		if content == "" {
			return "", fmt.Errorf("skip")
		}
		ts := TruncateTimestamp(getStringField(data, "timestamp"), DateTimeDisplayLen)
		if ts == "" {
			ts = "(no date)"
		}
		return fmt.Sprintf("- [%s] %s", ts, content), nil
	})
	if err != nil {
		return "", WrapFirestoreIndexError(err)
	}
	if len(signals) == 0 {
		return "", nil
	}
	return strings.Join(signals, "\n"), nil
}

// ListKnowledgeNodes lists all knowledge nodes (for diagnostics).
func ListKnowledgeNodes(ctx context.Context, limit int) ([]KnowledgeNode, error) {
	LoggerFrom(ctx).Info("listing knowledge nodes", "collection", KnowledgeCollection, "limit", limit)

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		LoggerFrom(ctx).Error("failed to get firestore client", "error", err)
		return nil, err
	}

	// First, just count documents to verify the collection is accessible
	iter := client.Collection(KnowledgeCollection).Limit(limit).Documents(ctx)
	defer iter.Stop()

	var nodes []KnowledgeNode
	docCount := 0
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			LoggerFrom(ctx).Info("iteration done", "docs_found", docCount)
			break
		}
		if err != nil {
			LoggerFrom(ctx).Error("error iterating knowledge nodes", "error", err, "docs_so_far", docCount)
			return nil, err
		}
		docCount++
		LoggerFrom(ctx).Debug("found document", "doc_id", doc.Ref.ID, "doc_path", doc.Ref.Path)

		// Try to get raw data first
		data := doc.Data()
		LoggerFrom(ctx).Debug("document data keys", "keys", fmt.Sprintf("%v", slices.Collect(maps.Keys(data))))

		var n KnowledgeNode
		if err := doc.DataTo(&n); err != nil {
			LoggerFrom(ctx).Warn("failed to parse knowledge node", "doc_id", doc.Ref.ID, "error", err)
			// Still try to extract basic fields manually
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

	LoggerFrom(ctx).Info("knowledge nodes listed", "docs_found", docCount, "nodes_parsed", len(nodes))
	return nodes, nil
}
