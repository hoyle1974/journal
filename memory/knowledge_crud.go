// Package memory — collection constants, node structs, and base CRUD helpers.
package memory

import (
	"context"
	"fmt"
	"strings"

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

// KnowledgeNode represents an arbitrary piece of structured data (Person, Task, Goal, Fact).
type KnowledgeNode struct {
	UUID            string   `firestore:"-" json:"uuid"`
	Content         string   `firestore:"content" json:"content"`
	NodeType        string   `firestore:"node_type" json:"node_type"`
	Metadata        string   `firestore:"metadata" json:"metadata"`
	Timestamp       string   `firestore:"timestamp" json:"timestamp"`
	JournalEntryIDs []string `firestore:"journal_entry_ids,omitempty" json:"journal_entry_ids,omitempty"`
	// SPO triple fields. Predicate is non-empty only for relational nodes extracted in
	// Subject | Predicate | Object format (e.g. "prefers", "works_at", "is_part_of").
	// ObjectUUID is the UUID of the object entity node when it corresponds to an existing
	// knowledge node; empty when the object is a raw string with no node.
	Predicate     string `firestore:"predicate,omitempty" json:"predicate,omitempty"`
	ObjectUUID    string `firestore:"object_uuid,omitempty" json:"object_uuid,omitempty"`
	SubjectUUID   string `firestore:"subject_uuid,omitempty" json:"subject_uuid,omitempty"`
	SourceEntryID string `firestore:"source_entry_uuid,omitempty" json:"source_entry_uuid,omitempty"`
	// Embedding is the vector representation of this node, populated on all reads.
	// omitempty prevents serializing the vector in JSON tool output.
	Embedding []float32 `firestore:"embedding" json:"embedding,omitempty"`
	// Loom graph caching fields (top-level Firestore fields for queryability during
	// hot-edge eviction and nightly decay). Present on relationship and object nodes.
	RelevanceScore     float64  `firestore:"relevance_score,omitempty"     json:"relevance_score,omitempty"`
	HotEdges           []string `firestore:"hot_edges,omitempty"           json:"hot_edges,omitempty"`
	LogicTrace         string   `firestore:"logic_trace,omitempty"         json:"logic_trace,omitempty"`
	SignificanceWeight float64  `firestore:"significance_weight,omitempty" json:"significance_weight,omitempty"`
	// QueryScore is the cosine similarity [0,1] from the most recent vector search.
	// Transient — never persisted to Firestore.
	QueryScore float64 `firestore:"-" json:"query_score,omitempty"`
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
	return nodeFromDoc(doc)
}

// FindNearestByType returns the single nearest knowledge node of the given node_type within distanceThreshold, else nil.
func (s *Store) FindNearestByType(ctx context.Context, queryVector []float32, nodeType string, distanceThreshold float64) (*KnowledgeNode, error) {
	vectorQuery := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", nodeType).
		FindNearest("embedding", firestore.Vector32(queryVector), 1, firestore.DistanceMeasureCosine,
			&firestore.FindNearestOptions{DistanceThreshold: &distanceThreshold})
	iter := vectorQuery.Documents(ctx)
	doc, err := iter.Next()
	iter.Stop()
	if err != nil || doc == nil {
		return nil, nil
	}
	return nodeFromDoc(doc)
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

// GetKnowledgeNodeByID loads one document from KnowledgeCollection and returns it with entity_links and journal_entry_ids.
func (s *Store) GetKnowledgeNodeByID(ctx context.Context, id string) (*KnowledgeNodeWithLinks, error) {
	doc, err := s.db.Collection(KnowledgeCollection).Doc(id).Get(ctx)
	if err != nil {
		return nil, err
	}
	n, err := nodeWithLinksFromDoc(doc)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// GetKnowledgeNodesByIDs fetches multiple knowledge nodes by UUID using Firestore
// GetAll for batched reads (up to 100 per RPC). Returns KnowledgeNodeWithLinks so
// EntityLinks are available for graph traversal at every hop.
func (s *Store) GetKnowledgeNodesByIDs(ctx context.Context, ids []string) ([]KnowledgeNodeWithLinks, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// Deduplicate.
	seen := make(map[string]bool, len(ids))
	deduped := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			deduped = append(deduped, id)
		}
	}

	const batchSize = 100
	nodes := make([]KnowledgeNodeWithLinks, 0, len(deduped))
	for i := 0; i < len(deduped); i += batchSize {
		end := i + batchSize
		if end > len(deduped) {
			end = len(deduped)
		}
		chunk := deduped[i:end]

		refs := make([]*firestore.DocumentRef, len(chunk))
		for j, id := range chunk {
			refs[j] = s.db.Collection(KnowledgeCollection).Doc(id)
		}

		docs, err := s.db.GetAll(ctx, refs)
		if err != nil {
			return nil, fmt.Errorf("batch get knowledge nodes: %w", err)
		}

		for _, doc := range docs {
			if !doc.Exists() {
				s.log.Debug("get knowledge nodes batch: doc not found", "id", doc.Ref.ID)
				continue
			}
			n, err := nodeWithLinksFromDoc(doc)
			if err != nil {
				s.log.Debug("get knowledge nodes batch: deserialise failed", "id", doc.Ref.ID, "err", err)
				continue
			}
			nodes = append(nodes, n)
		}
	}
	return nodes, nil
}

// GetUserIdentityNodes returns knowledge nodes of type user_identity, for easy retrieval of self-referential identity statements.
func (s *Store) GetUserIdentityNodes(ctx context.Context, limit int) ([]KnowledgeNode, error) {
	query := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeUserIdentity).
		OrderBy("timestamp", firestore.Desc).
		Limit(limit)
	nodes, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (KnowledgeNode, error) {
		n, err := nodeFromDoc(doc)
		if err != nil {
			return KnowledgeNode{}, err
		}
		return *n, nil
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
