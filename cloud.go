package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/google/uuid"
	"github.com/jackstrohm/jot/internal/memory"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// WrapFirestoreIndexError wraps Firestore "query requires an index" errors with a user-facing
// message and deploy instructions. The console link in the raw error often does not work.
func WrapFirestoreIndexError(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) != codes.FailedPrecondition {
		return err
	}
	if !strings.Contains(err.Error(), "index") {
		return err
	}
	return fmt.Errorf("Firestore query requires a composite index. Add the needed index to firestore.indexes.json and run: firebase deploy --only firestore:indexes — %w", err)
}

// GetFirestoreClient returns the Firestore client from the App in context.
// Callers must use a context that has App attached (e.g. from an HTTP request).
// For non-HTTP code (e.g. CLI tools), create an App with NewApp and attach with WithApp.
func GetFirestoreClient(ctx context.Context) (*firestore.Client, error) {
	app := GetApp(ctx)
	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}
	return app.Firestore(ctx)
}

// GenerateUUID creates a new UUID for entries/todos.
func GenerateUUID() string {
	return uuid.New().String()
}

// =============================================================================
// ENTRY TYPES
// =============================================================================

// Entry represents a journal entry.
type Entry struct {
	UUID      string `firestore:"-" json:"uuid"`
	Content   string `firestore:"content" json:"content"`
	Source    string `firestore:"source" json:"source"`
	Timestamp string `firestore:"timestamp" json:"timestamp"`
}

// =============================================================================
// ENTRY OPERATIONS
// =============================================================================

// AddEntry adds a new entry to Firestore. Returns the entry UUID.
func AddEntry(ctx context.Context, content, source string, timestamp *string) (string, error) {
	if content == "" {
		return "", fmt.Errorf("content is required and must be a non-empty string")
	}
	if source == "" {
		return "", fmt.Errorf("source is required and must be a string")
	}

	entryUUID := GenerateUUID()
	ts := time.Now().Format(time.RFC3339)
	if timestamp != nil && *timestamp != "" {
		ts = *timestamp
	}

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return "", err
	}

	_, err = client.Collection(EntriesCollection).Doc(entryUUID).Set(ctx, map[string]interface{}{
		"content":   content,
		"source":    source,
		"timestamp": ts,
	})
	if err != nil {
		return "", err
	}

	payload := map[string]interface{}{
		"uuid":      entryUUID,
		"content":   content,
		"timestamp": ts,
		"source":    source,
	}
	if err := EnqueueTask(ctx, "/internal/process-entry", payload); err != nil {
		LoggerFrom(ctx).Warn("failed to enqueue process-entry task, running inline", "entry_uuid", entryUUID, "error", err)
		// Run process-entry inline (e.g. when JOT_API_URL is not set or Cloud Tasks unavailable)
		app := GetApp(ctx)
		if app != nil {
			bgCtx := WithApp(context.Background(), app)
			SubmitAsync(ctx, func() {
				_ = ProcessEntry(bgCtx, entryUUID, content, ts, source)
			})
		}
	}

	return entryUUID, nil
}

// GetEntries fetches entries from Firestore, ordered by timestamp descending.
func GetEntries(ctx context.Context, limit int) ([]Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var entries []Entry
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var e Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}
		e.UUID = doc.Ref.ID
		entries = append(entries, e)
	}
	return entries, nil
}

// GetEntriesAsc fetches entries from Firestore, ordered by timestamp ascending (oldest first).
// Use this when the user asks for the "oldest" or "earliest" entry or memory.
func GetEntriesAsc(ctx context.Context, limit int) ([]Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Asc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var entries []Entry
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var e Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}
		e.UUID = doc.Ref.ID
		entries = append(entries, e)
	}
	return entries, nil
}

// GetEntriesByDateRange fetches entries within a date range.
func GetEntriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	// Normalize dates to include full day
	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}

	iter := client.Collection(EntriesCollection).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var entries []Entry
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, WrapFirestoreIndexError(err)
		}

		var e Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}
		e.UUID = doc.Ref.ID
		entries = append(entries, e)
	}
	return entries, nil
}

// SearchEntries searches entries containing keywords (case-insensitive).
func SearchEntries(ctx context.Context, keywords string, limit int) ([]Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(500).
		Documents(ctx)
	defer iter.Stop()

	keywordsLower := strings.Fields(strings.ToLower(keywords))
	var entries []Entry

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var e Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}

		contentLower := strings.ToLower(e.Content)
		allMatch := true
		for _, kw := range keywordsLower {
			if !strings.Contains(contentLower, kw) {
				allMatch = false
				break
			}
		}

		if allMatch {
			e.UUID = doc.Ref.ID
			entries = append(entries, e)
			if len(entries) >= limit {
				break
			}
		}
	}
	return entries, nil
}

// CountEntries counts entries, optionally within a date range.
func CountEntries(ctx context.Context, startDate, endDate *string) (int, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return 0, err
	}

	var query firestore.Query
	if startDate != nil && endDate != nil && *startDate != "" && *endDate != "" {
		start := *startDate
		end := *endDate
		if len(start) == 10 {
			start = start + "T00:00:00"
		}
		if len(end) == 10 {
			end = end + "T23:59:59"
		}
		query = client.Collection(EntriesCollection).
			Where("timestamp", ">=", start).
			Where("timestamp", "<=", end)
	} else {
		query = client.Collection(EntriesCollection).Query
	}

	iter := query.Documents(ctx)
	defer iter.Stop()

	count := 0
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, WrapFirestoreIndexError(err)
		}
		count++
	}
	return count, nil
}

// GetUniqueSources gets all unique sources from entries.
func GetUniqueSources(ctx context.Context) ([]string, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).Limit(1000).Documents(ctx)
	defer iter.Stop()

	sources := make(map[string]bool)
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		data := doc.Data()
		if source, ok := data["source"].(string); ok && source != "" {
			sources[source] = true
		}
	}

	result := make([]string, 0, len(sources))
	for s := range sources {
		result = append(result, s)
	}
	sort.Strings(result)
	return result, nil
}

// GetEntriesBySource gets entries filtered by source (partial match).
func GetEntriesBySource(ctx context.Context, sourceFilter string, limit int) ([]Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(500).
		Documents(ctx)
	defer iter.Stop()

	sourceFilterLower := strings.ToLower(sourceFilter)
	var entries []Entry

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var e Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}

		if strings.Contains(strings.ToLower(e.Source), sourceFilterLower) {
			e.UUID = doc.Ref.ID
			entries = append(entries, e)
			if len(entries) >= limit {
				break
			}
		}
	}
	return entries, nil
}

// GetEntry fetches a single entry by UUID.
func GetEntry(ctx context.Context, entryUUID string) (*Entry, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	doc, err := client.Collection(EntriesCollection).Doc(entryUUID).Get(ctx)
	if err != nil {
		return nil, err
	}

	var e Entry
	if err := doc.DataTo(&e); err != nil {
		return nil, err
	}
	e.UUID = doc.Ref.ID
	return &e, nil
}

// UpdateEntry updates an entry's content.
func UpdateEntry(ctx context.Context, entryUUID, newContent string) error {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}

	_, err = client.Collection(EntriesCollection).Doc(entryUUID).Update(ctx, []firestore.Update{
		{Path: "content", Value: newContent},
	})
	return err
}

// DeleteEntry deletes a single entry.
func DeleteEntry(ctx context.Context, entryUUID string) error {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}

	_, err = client.Collection(EntriesCollection).Doc(entryUUID).Delete(ctx)
	return err
}

// DeleteEntries deletes multiple entries.
func DeleteEntries(ctx context.Context, entryUUIDs []string) error {
	if len(entryUUIDs) == 0 {
		return nil
	}

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}

	batch := client.Batch()
	for _, uuid := range entryUUIDs {
		batch.Delete(client.Collection(EntriesCollection).Doc(uuid))
	}
	_, err = batch.Commit(ctx)
	return err
}

// GetDatesWithEntries gets list of dates that have entries.
func GetDatesWithEntries(ctx context.Context) ([]string, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Asc).
		Documents(ctx)
	defer iter.Stop()

	dates := make(map[string]bool)
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		data := doc.Data()
		if ts, ok := data["timestamp"].(string); ok && len(ts) >= 10 {
			dates[ts[:10]] = true
		}
	}

	result := make([]string, 0, len(dates))
	for d := range dates {
		result = append(result, d)
	}
	sort.Strings(result)
	return result, nil
}

// =============================================================================
// QUERY LOG TYPES
// =============================================================================

// QueryLog represents a logged query.
type QueryLog struct {
	UUID      string `firestore:"-" json:"uuid"`
	Question  string `firestore:"question" json:"question"`
	Answer    string `firestore:"answer" json:"answer"`
	Source    string `firestore:"source" json:"source"`
	Timestamp string `firestore:"timestamp" json:"timestamp"`
	IsGap     bool   `firestore:"is_gap" json:"is_gap"` // true when search tools returned no results (knowledge gap)
}

// =============================================================================
// QUERY LOG OPERATIONS
// =============================================================================

// SaveQuery saves a query and its response. If isGap is true, the query is recorded as a knowledge gap (search returned no results).
func SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return "", err
	}

	queryUUID := GenerateUUID()
	timestamp := time.Now().Format(time.RFC3339)

	doc := map[string]interface{}{
		"question":  question,
		"answer":    answer,
		"source":    source,
		"timestamp": timestamp,
		"is_gap":    isGap,
	}
	if isGap && !strings.Contains(strings.ToLower(answer), "looked for this but found nothing") {
		doc["answer"] = answer + "\n\n(I looked for this but found nothing.)"
	}

	_, err = client.Collection(QueriesCollection).Doc(queryUUID).Set(ctx, doc)
	if err != nil {
		return "", err
	}

	return queryUUID, nil
}

// GetRecentQueries gets the most recent queries.
func GetRecentQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(QueriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var queries []QueryLog
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			continue
		}
		q.UUID = doc.Ref.ID
		queries = append(queries, q)
	}
	return queries, nil
}

// SearchQueries searches past queries by keywords.
func SearchQueries(ctx context.Context, keywords string, limit int) ([]QueryLog, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(QueriesCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(200).
		Documents(ctx)
	defer iter.Stop()

	keywordsLower := strings.Fields(strings.ToLower(keywords))
	var queries []QueryLog

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			continue
		}

		questionLower := strings.ToLower(q.Question)
		allMatch := true
		for _, kw := range keywordsLower {
			if !strings.Contains(questionLower, kw) {
				allMatch = false
				break
			}
		}

		if allMatch {
			q.UUID = doc.Ref.ID
			queries = append(queries, q)
			if len(queries) >= limit {
				break
			}
		}
	}
	return queries, nil
}

// GetRecentGapQueries returns the most recent queries that were marked as knowledge gaps (no results found).
func GetRecentGapQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	iter := client.Collection(QueriesCollection).
		Where("is_gap", "==", true).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var queries []QueryLog
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, WrapFirestoreIndexError(err)
		}

		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			continue
		}
		q.UUID = doc.Ref.ID
		queries = append(queries, q)
	}
	return queries, nil
}

// GetQueriesByDateRange gets queries within a date range.
func GetQueriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]QueryLog, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	if len(startDate) == 10 {
		startDate = startDate + "T00:00:00"
	}
	if len(endDate) == 10 {
		endDate = endDate + "T23:59:59"
	}

	iter := client.Collection(QueriesCollection).
		Where("timestamp", ">=", startDate).
		Where("timestamp", "<=", endDate).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var queries []QueryLog
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, WrapFirestoreIndexError(err)
		}

		var q QueryLog
		if err := doc.DataTo(&q); err != nil {
			continue
		}
		q.UUID = doc.Ref.ID
		queries = append(queries, q)
	}
	return queries, nil
}

// =============================================================================
// KNOWLEDGE NODE TYPES (Vector-backed Long-Term Memory)
// =============================================================================

const KnowledgeCollection = "knowledge_nodes"

// KnowledgeNode represents an arbitrary piece of structured data (Person, Task, Goal, Fact).
type KnowledgeNode struct {
	UUID      string `firestore:"-" json:"uuid"`
	Content   string `firestore:"content" json:"content"`
	NodeType  string `firestore:"node_type" json:"node_type"` // e.g., "list", "person", "fact"
	Metadata  string `firestore:"metadata" json:"metadata"`   // JSON string of relationships/attributes
	Timestamp string `firestore:"timestamp" json:"timestamp"`
	// Note: embedding field is excluded from struct as it causes decoding issues with Firestore's vector type
}

// KnowledgeNodeWithLinks extends KnowledgeNode with entity_links and journal_entry_ids for graph traversal.
type KnowledgeNodeWithLinks struct {
	KnowledgeNode
	EntityLinks      []string // UUIDs of related knowledge nodes
	JournalEntryIDs  []string // UUIDs of source journal entries
}

// =============================================================================
// KNOWLEDGE NODE OPERATIONS
// =============================================================================

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
		// Found a very similar existing node - update it
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
		nodeUUID = doc.Ref.ID
		_, err = client.Collection(KnowledgeCollection).Doc(nodeUUID).Set(ctx, data)
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
			UUID:      doc.Ref.ID,
			Content:   getStringField(data, "content"),
			NodeType:  getStringField(data, "node_type"),
			Metadata:  getStringField(data, "metadata"),
			Timestamp: getStringField(data, "timestamp"),
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

	iter := client.Collection(KnowledgeCollection).
		OrderBy("timestamp", firestore.Desc).
		Limit(500).
		Documents(ctx)
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
		content := getStringField(data, "content")
		metadata := getStringField(data, "metadata")
		contentLower := strings.ToLower(content)
		metadataLower := strings.ToLower(metadata)

		allMatch := true
		for _, kw := range keywordsLower {
			if !strings.Contains(contentLower, kw) && !strings.Contains(metadataLower, kw) {
				allMatch = false
				break
			}
		}

		if allMatch {
			n := KnowledgeNode{
				UUID:      doc.Ref.ID,
				Content:   content,
				NodeType:  getStringField(data, "node_type"),
				Metadata:  metadata,
				Timestamp: getStringField(data, "timestamp"),
			}
			nodes = append(nodes, n)
			if len(nodes) >= limit {
				break
			}
		}
	}
	return nodes, nil
}

// getStringSliceField parses a Firestore array of strings (or interface{} elements) into []string.
func getStringSliceField(data map[string]interface{}, field string) []string {
	v, ok := data[field].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(v))
	for _, e := range v {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
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
	iter := client.Collection(KnowledgeCollection).
		Where("domain", "==", "selfmodel").
		Where("node_type", "==", "thought").
		OrderBy("timestamp", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var signals []string
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return "", WrapFirestoreIndexError(err)
		}
		data := doc.Data()
		content := getStringField(data, "content")
		if content != "" {
			ts := getStringField(data, "timestamp")
			if len(ts) > 19 {
				ts = ts[:19]
			}
			if ts == "" {
				ts = "(no date)"
			}
			signals = append(signals, fmt.Sprintf("- [%s] %s", ts, content))
		}
	}
	if len(signals) == 0 {
		return "", nil
	}
	return strings.Join(signals, "\n"), nil
}

// QuerySimilarEntries performs a KNN vector search on journal entries.
// Requires a Firestore vector index on entries.embedding (768 dimensions).
func QuerySimilarEntries(ctx context.Context, queryVector []float32, limit int) ([]Entry, error) {
	ctx, span := StartSpan(ctx, "entries.query_similar")
	defer span.End()

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	vectorQuery := client.Collection(EntriesCollection).
		FindNearest("embedding", firestore.Vector32(queryVector), limit, firestore.DistanceMeasureCosine, nil)

	iter := vectorQuery.Documents(ctx)
	defer iter.Stop()

	var entries []Entry
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			LoggerFrom(ctx).Error("entry vector search error", "error", err)
			span.RecordError(err)
			return nil, err
		}

		data := doc.Data()
		e := Entry{
			UUID:      doc.Ref.ID,
			Content:   getStringField(data, "content"),
			Source:    getStringField(data, "source"),
			Timestamp: getStringField(data, "timestamp"),
		}
		entries = append(entries, e)
	}

	span.SetAttributes(map[string]string{
		"results_count": fmt.Sprintf("%d", len(entries)),
	})
	return entries, nil
}

// BackfillEntryEmbeddings finds entries without embeddings, generates them, and updates docs.
// Processes up to limit entries per call. Returns number processed and any error.
func BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error) {
	ctx, span := StartSpan(ctx, "entries.backfill_embeddings")
	defer span.End()

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return 0, err
	}

	iter := client.Collection(EntriesCollection).
		OrderBy("timestamp", firestore.Asc).
		Limit(500).
		Documents(ctx)
	defer iter.Stop()

	processed := 0
	for processed < limit {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return processed, err
		}

		data := doc.Data()
		if _, has := data["embedding"]; has {
			continue
		}

		content := getStringField(data, "content")
		if content == "" {
			continue
		}

		vector, err := GenerateEmbedding(ctx, content, EmbedTaskRetrievalDocument)
		if err != nil {
			LoggerFrom(ctx).Warn("backfill embedding failed", "doc", doc.Ref.ID, "error", err)
			continue
		}

		_, err = client.Collection(EntriesCollection).Doc(doc.Ref.ID).Update(ctx, []firestore.Update{
			{Path: "embedding", Value: firestore.Vector32(vector)},
		})
		if err != nil {
			LoggerFrom(ctx).Warn("backfill update failed", "doc", doc.Ref.ID, "error", err)
			continue
		}
		processed++
		LoggerFrom(ctx).Debug("backfill embedded entry", "doc", doc.Ref.ID)
	}

	span.SetAttributes(map[string]string{"processed": fmt.Sprintf("%d", processed)})
	return processed, nil
}

func getStringField(data map[string]interface{}, field string) string {
	if v, ok := data[field].(string); ok {
		return v
	}
	return ""
}

func truncateForLog(s string, maxLen int) string {
	if len([]rune(s)) <= maxLen {
		return s
	}
	return SafeTruncate(s, maxLen) + "..."
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
