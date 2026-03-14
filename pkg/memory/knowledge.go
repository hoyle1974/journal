// Package memory provides knowledge node types and operations (vector-backed long-term memory).
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/api/iterator"
)

// KnowledgeCollection is the Firestore collection name for knowledge nodes.
const KnowledgeCollection = "knowledge_nodes"

// KnowledgeNode represents an arbitrary piece of structured data (Person, Task, Goal, Fact).
type KnowledgeNode struct {
	UUID            string   `firestore:"-" json:"uuid"`
	Content         string   `firestore:"content" json:"content"`
	NodeType        string   `firestore:"node_type" json:"node_type"`
	Metadata        string   `firestore:"metadata" json:"metadata"`
	Timestamp       string   `firestore:"timestamp" json:"timestamp"`
	JournalEntryIDs []string `firestore:"-" json:"journal_entry_ids,omitempty"`
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
	return utils.TruncateString(s, maxLen) + "..."
}

// UpsertKnowledge saves a fact/list item and computes its vector embedding automatically.
func UpsertKnowledge(ctx context.Context, content, nodeType, metadata string, journalEntryIDs []string) (string, error) {
	ctx, span := infra.StartSpan(ctx, "knowledge.upsert")
	defer span.End()

	infra.LoggerFrom(ctx).Info("upserting knowledge", "content", truncateForLog(content, 50), "node_type", nodeType)

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
			infra.LoggerFrom(ctx).Warn("metadata validation failed", "node_type", nodeType, "error", err)
			span.RecordError(err)
			return "", fmt.Errorf("invalid metadata for node_type %q: %w", nodeType, err)
		}
		normalized, err := NormalizeMetadata(nodeType, m)
		if err != nil {
			span.RecordError(err)
			return "", fmt.Errorf("normalize metadata: %w", err)
		}
		metaToStore, err = MetadataToJSON(normalized)
		if err != nil {
			span.RecordError(err)
			return "", err
		}
	}

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to get firestore client", "error", err)
		span.RecordError(err)
		return "", err
	}

	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return "", fmt.Errorf("no app in context")
	}
	projectID := app.Config().GoogleCloudProject
	vector, err := infra.GenerateEmbedding(ctx, projectID, content+" "+metaToStore, infra.EmbedTaskRetrievalDocument)
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to generate embedding", "error", err)
		span.RecordError(err)
		return "", err
	}
	infra.LoggerFrom(ctx).Debug("embedding generated", "dimensions", len(vector))

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
	vectorQuery := client.Collection(KnowledgeCollection).
		FindNearest("embedding", firestore.Vector32(vector), 1, firestore.DistanceMeasureCosine,
			&firestore.FindNearestOptions{DistanceThreshold: &distanceThreshold})

	iter := vectorQuery.Documents(ctx)
	doc, err := iter.Next()
	iter.Stop()

	var nodeUUID string
	if err == nil && doc != nil {
		existingContent := infra.GetStringField(doc.Data(), "content")
		action, collErr := infra.EvaluateFactCollision(ctx, app.Config(), content, existingContent)
		if collErr != nil {
			infra.LoggerFrom(ctx).Warn("fact collision check failed, inserting new node", "error", collErr)
			action = "insert"
		}
		if action == "update" {
			nodeUUID = doc.Ref.ID
			infra.LoggerFrom(ctx).Info("updating existing knowledge node", "uuid", nodeUUID)
			_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
			if err != nil {
				infra.LoggerFrom(ctx).Error("failed to update knowledge node", "error", err)
				span.RecordError(err)
				return "", err
			}
		} else {
			nodeUUID = infra.GenerateUUID()
			_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
			if err != nil {
				span.RecordError(err)
				return "", err
			}
			infra.LoggerFrom(ctx).Info("knowledge node created", "uuid", nodeUUID)
		}
	} else {
		nodeUUID = infra.GenerateUUID()
		_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
		if err != nil {
			span.RecordError(err)
			return "", err
		}
		infra.LoggerFrom(ctx).Info("knowledge node created", "uuid", nodeUUID)
	}

	span.SetAttributes(map[string]string{"node_uuid": nodeUUID, "node_type": nodeType})
	return nodeUUID, nil
}

// UpsertSemanticMemory saves a fact with extended schema (significance, domain, etc.).
func UpsertSemanticMemory(ctx context.Context, content, nodeType, domain string, significanceWeight float64, entityLinks []string, journalEntryIDs []string) (string, error) {
	ctx, span := infra.StartSpan(ctx, "semantic.upsert")
	defer span.End()

	metadata := fmt.Sprintf(`{"domain":"%s"}`, domain)
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return "", err
	}
	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return "", fmt.Errorf("no app in context")
	}
	projectID := app.Config().GoogleCloudProject
	vector, err := infra.GenerateEmbedding(ctx, projectID, content+" "+metadata, infra.EmbedTaskRetrievalDocument)
	if err != nil {
		return "", err
	}

	timestamp := time.Now().Format(time.RFC3339)
	now := timestamp
	distanceThreshold := 0.25
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
		existingContent := infra.GetStringField(doc.Data(), "content")
		action, collErr := infra.EvaluateFactCollision(ctx, app.Config(), content, existingContent)
		if collErr != nil {
			infra.LoggerFrom(ctx).Warn("fact collision check failed, inserting new node", "error", collErr)
			action = "insert"
		}
		if action == "update" {
			nodeUUID = doc.Ref.ID
			_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
		} else {
			nodeUUID = infra.GenerateUUID()
			_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
		}
	} else {
		nodeUUID = infra.GenerateUUID()
		_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
	}
	if err != nil {
		span.RecordError(err)
		return "", err
	}
	return nodeUUID, nil
}

// FindNearestWithThreshold returns the single nearest knowledge node if within distanceThreshold, else nil.
func FindNearestWithThreshold(ctx context.Context, queryVector []float32, distanceThreshold float64) (*KnowledgeNode, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	vectorQuery := client.Collection(KnowledgeCollection).
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
		Content:         infra.GetStringField(data, "content"),
		NodeType:        infra.GetStringField(data, "node_type"),
		Metadata:        infra.GetStringField(data, "metadata"),
		Timestamp:       infra.GetStringField(data, "timestamp"),
		JournalEntryIDs: infra.GetStringSliceField(data, "journal_entry_ids"),
	}
	return n, nil
}

// AppendJournalEntryIDsToNode merges entryIDs into the node's journal_entry_ids (deduped) and updates the document.
func AppendJournalEntryIDsToNode(ctx context.Context, nodeUUID string, entryIDs []string) error {
	if len(entryIDs) == 0 {
		return nil
	}
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	doc, err := client.Collection(KnowledgeCollection).Doc(nodeUUID).Get(ctx)
	if err != nil {
		return err
	}
	existing := infra.GetStringSliceField(doc.Data(), "journal_entry_ids")
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
	_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Update(ctx, []firestore.Update{
		{Path: "journal_entry_ids", Value: existing},
	})
	return err
}

// AddEntityLink appends a target UUID (e.g. a fact or project node) to a source node's entity_links.
// Idempotent: if targetUUID is already in the list, no update is performed.
func AddEntityLink(ctx context.Context, sourceUUID, targetUUID string) error {
	if sourceUUID == "" || targetUUID == "" {
		return nil
	}
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	return client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ref := client.Collection(KnowledgeCollection).Doc(sourceUUID)
		doc, err := tx.Get(ref)
		if err != nil {
			return err
		}
		links := infra.GetStringSliceField(doc.Data(), "entity_links")
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
func QuerySimilarNodes(ctx context.Context, queryVector []float32, limit int) ([]KnowledgeNode, error) {
	ctx, span := infra.StartSpan(ctx, "knowledge.query_similar")
	defer span.End()

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	infra.LoggerFrom(ctx).Debug("vector search starting", "collection", KnowledgeCollection, "vector_dims", len(queryVector), "limit", limit)

	// Request distance in result so we can log similarity score (1 - cosine distance).
	const distanceResultField = "_vector_distance"
	opts := &firestore.FindNearestOptions{DistanceResultField: distanceResultField}
	vectorQuery := client.Collection(KnowledgeCollection).
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
			infra.LogVectorSearchFailed(ctx, KnowledgeCollection, err, 0)
			span.RecordError(err)
			return nil, err
		}
		data := doc.Data()
		n := KnowledgeNode{
			UUID:            doc.Ref.ID,
			Content:         infra.GetStringField(data, "content"),
			NodeType:        infra.GetStringField(data, "node_type"),
			Metadata:        infra.GetStringField(data, "metadata"),
			Timestamp:       infra.GetStringField(data, "timestamp"),
			JournalEntryIDs: infra.GetStringSliceField(data, "journal_entry_ids"),
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
		infra.LogFoundNode(ctx, n.UUID, score, n.Content)
	}

	infra.LogRAGQuality(ctx, limit, scores)
	span.SetAttributes(map[string]string{"results_count": fmt.Sprintf("%d", len(nodes))})
	return nodes, nil
}

// SearchKnowledgeNodes searches knowledge nodes by keywords (case-insensitive) in Content and Metadata.
func SearchKnowledgeNodes(ctx context.Context, keywords string, limit int) ([]KnowledgeNode, error) {
	client, err := infra.GetFirestoreClient(ctx)
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
	nodes, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		data := doc.Data()
		content := infra.GetStringField(data, "content")
		metadata := infra.GetStringField(data, "metadata")
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
			NodeType:        infra.GetStringField(data, "node_type"),
			Metadata:        metadata,
			Timestamp:       infra.GetStringField(data, "timestamp"),
			JournalEntryIDs: infra.GetStringSliceField(data, "journal_entry_ids"),
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
	client, err := infra.GetFirestoreClient(ctx)
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
			Content:   infra.GetStringField(data, "content"),
			NodeType:  infra.GetStringField(data, "node_type"),
			Metadata:  infra.GetStringField(data, "metadata"),
			Timestamp: infra.GetStringField(data, "timestamp"),
		},
		EntityLinks:     infra.GetStringSliceField(data, "entity_links"),
		JournalEntryIDs: infra.GetStringSliceField(data, "journal_entry_ids"),
	}
	return n, nil
}

// GetKnowledgeNodesByIDs fetches multiple knowledge nodes by UUID.
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
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	var nodes []KnowledgeNode
	for _, id := range deduped {
		doc, err := client.Collection(KnowledgeCollection).Doc(id).Get(ctx)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("get knowledge node by id skip", "id", id, "error", err)
			continue
		}
		data := doc.Data()
		n := KnowledgeNode{
			UUID:            doc.Ref.ID,
			Content:         infra.GetStringField(data, "content"),
			NodeType:        infra.GetStringField(data, "node_type"),
			Metadata:        infra.GetStringField(data, "metadata"),
			Timestamp:       infra.GetStringField(data, "timestamp"),
			JournalEntryIDs: infra.GetStringSliceField(data, "journal_entry_ids"),
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// FindEntityNodeByName does an embedding search for a person/entity by name.
func FindEntityNodeByName(ctx context.Context, entityName string) (*KnowledgeNode, error) {
	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("no app in context")
	}
	projectID := app.Config().GoogleCloudProject
	query := "Person: " + entityName + " relationship"
	vec, err := infra.GenerateEmbedding(ctx, projectID, query)
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
	for i := range nodes {
		if nodes[i].NodeType == "person" {
			return &nodes[i], nil
		}
	}
	return nil, nil
}

// FindProjectOrGoalByName finds the nearest project or goal knowledge node by semantic similarity to the given name.
// Returns nil if no project/goal node is found within the search results.
func FindProjectOrGoalByName(ctx context.Context, projectName string) (*KnowledgeNode, error) {
	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("no app in context")
	}
	projectID := app.Config().GoogleCloudProject
	vec, err := infra.GenerateEmbedding(ctx, projectID, "Project: "+projectName)
	if err != nil {
		return nil, err
	}
	nodes, err := QuerySimilarNodes(ctx, vec, 5)
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
func UpdateProjectStatus(ctx context.Context, nodeID, status string) error {
	ctx, span := infra.StartSpan(ctx, "knowledge.update_project_status")
	defer span.End()

	node, err := GetKnowledgeNodeByID(ctx, nodeID)
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

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(KnowledgeCollection).Doc(nodeID).Update(ctx, []firestore.Update{
		{Path: "metadata", Value: metaJSON},
		{Path: "last_recalled_at", Value: time.Now().Format(time.RFC3339)},
	})
	if err != nil {
		span.RecordError(err)
		return err
	}
	span.SetAttributes(map[string]string{"node_id": nodeID, "status": status})
	return nil
}

// DiscoverRelatedNodes finds nodes semantically related to an entity name that may not be in entity_links.
// Excludes person nodes whose content contains the entity name (to avoid returning the primary entity card).
func DiscoverRelatedNodes(ctx context.Context, entityName string, limit int) ([]KnowledgeNode, error) {
	app := infra.GetApp(ctx)
	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("no app in context")
	}
	query := fmt.Sprintf("Facts and information about %s", entityName)
	vec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, query)
	if err != nil {
		return nil, err
	}
	candidates, err := QuerySimilarNodes(ctx, vec, limit*2)
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
func AppendToProjectArchiveSummary(ctx context.Context, projectID, oneLine string) error {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	doc, err := client.Collection(KnowledgeCollection).Doc(projectID).Get(ctx)
	if err != nil {
		return err
	}
	data := doc.Data()
	metadataStr := infra.GetStringField(data, "metadata")
	var meta map[string]interface{}
	if metadataStr != "" {
		_ = json.Unmarshal([]byte(metadataStr), &meta)
	}
	if meta == nil {
		meta = make(map[string]interface{})
	}
	current, _ := meta["archive_summary"].(string)
	if current == "" {
		current = infra.GetStringField(data, "archive_summary")
	}
	line := oneLine
	if len(line) > 200 {
		line = utils.TruncateToMaxBytes(line, 197) + "..."
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

// GetLinkedCompletedProjectID returns the ID of a completed project linked to this node, or "".
func GetLinkedCompletedProjectID(ctx context.Context, nodeData map[string]interface{}) string {
	metadataStr := infra.GetStringField(nodeData, "metadata")
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
	for _, id := range infra.GetStringSliceField(nodeData, "entity_links") {
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

// GetUserIdentityNodes returns knowledge nodes of type user_identity, for easy retrieval of self-referential identity statements.
func GetUserIdentityNodes(ctx context.Context, limit int) ([]KnowledgeNode, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeUserIdentity).
		OrderBy("timestamp", firestore.Desc).
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

// GetActiveSignals retrieves recent proactive signals (selfmodel thought nodes) for the FOH.
func GetActiveSignals(ctx context.Context, limit int) (string, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return "", err
	}
	query := client.Collection(KnowledgeCollection).
		Where("domain", "==", "selfmodel").
		Where("node_type", "==", "thought").
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	signals, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (string, error) {
		data := doc.Data()
		content := infra.GetStringField(data, "content")
		if content == "" {
			return "", fmt.Errorf("skip")
		}
		ts := infra.GetStringField(data, "timestamp")
		if len([]rune(ts)) > 19 {
			ts = utils.TruncateString(ts, 19)
		}
		if ts == "" {
			ts = "(no date)"
		}
		return fmt.Sprintf("- [%s] %s", ts, content), nil
	})
	if err != nil {
		return "", infra.WrapFirestoreIndexError(err)
	}
	if len(signals) == 0 {
		return "", nil
	}
	return strings.Join(signals, "\n"), nil
}

// ListKnowledgeNodes lists all knowledge nodes (for diagnostics).
func ListKnowledgeNodes(ctx context.Context, limit int) ([]KnowledgeNode, error) {
	infra.LoggerFrom(ctx).Info("listing knowledge nodes", "collection", KnowledgeCollection, "limit", limit)
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	iter := client.Collection(KnowledgeCollection).Limit(limit).Documents(ctx)
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
