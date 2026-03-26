package memory

import (
	"context"
	"time"
)

// Compile-time checks that *Store satisfies all domain interfaces.
var (
	_ EntryStore     = (*Store)(nil)
	_ KnowledgeStore = (*Store)(nil)
	_ GraphStore     = (*Store)(nil)
	_ TaskStore      = (*Store)(nil)
	_ AgentOps       = (*Store)(nil)
	_ AdminOps       = (*Store)(nil)
)

// List returns up to limit entries. When ascending is true the oldest entries are returned first.
func (s *Store) List(ctx context.Context, limit int, ascending bool) ([]Entry, error) {
	if ascending {
		return s.GetEntriesAsc(ctx, limit)
	}
	return s.GetEntries(ctx, limit)
}

// Search performs a keyword search over journal entries.
func (s *Store) Search(ctx context.Context, keywords string, limit int) ([]Entry, error) {
	return s.SearchEntries(ctx, keywords, limit)
}

// Delete removes the given entry UUIDs from the journal.
func (s *Store) Delete(ctx context.Context, uuids []string) error {
	return s.DeleteEntries(ctx, uuids)
}

// Upsert inserts or updates a knowledge node.
// If opts.Embedding is set it is used directly; otherwise an embedding is generated.
// If opts.SPO is set the node is stored with an SPO triple.
func (s *Store) Upsert(ctx context.Context, content, nodeType, domain string, weight float64, opts UpsertOptions) (string, error) {
	switch {
	case opts.Embedding != nil && opts.SPO != nil:
		return s.UpsertSemanticMemoryPreembeddedWithSPO(ctx, content, nodeType, domain, weight, opts.EntityLinks, opts.JournalEntryIDs, opts.Embedding, opts.SPO)
	case opts.Embedding != nil:
		return s.UpsertSemanticMemoryPreembedded(ctx, content, nodeType, domain, weight, opts.EntityLinks, opts.JournalEntryIDs, opts.Embedding)
	default:
		return s.UpsertSemanticMemory(ctx, content, nodeType, domain, weight, opts.EntityLinks, opts.JournalEntryIDs)
	}
}

// ExpandMulti performs a multi-hop BFS traversal from multiple seed nodes and
// returns a single normalized SubGraph with deduplicated nodes and edges.
func (s *Store) ExpandMulti(ctx context.Context, seedIDs []string, queryVector []float32, hops, limitPerEdge int) (*SubGraph, error) {
	return s.GraphExpandMulti(ctx, seedIDs, queryVector, hops, limitPerEdge)
}

// QuerySimilar performs a vector ANN search with optional significance filtering.
// opts.Limit defaults to 20 when <= 0.
func (s *Store) QuerySimilar(ctx context.Context, queryVector []float32, opts SearchOptions) ([]KnowledgeNode, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	return s.QuerySimilarSemanticNodes(ctx, queryVector, limit, opts.MinSignificance)
}

// SearchKeywords performs keyword search over knowledge nodes.
func (s *Store) SearchKeywords(ctx context.Context, keywords string, limit int) ([]KnowledgeNode, error) {
	return s.SearchKnowledgeNodes(ctx, keywords, limit)
}

// Rerank re-orders nodes by relevance to query using the LLM.
func (s *Store) Rerank(ctx context.Context, query string, nodes []KnowledgeNode, topN int) ([]KnowledgeNode, error) {
	return s.RerankNodes(ctx, query, nodes, topN)
}

// GetUnresolvedQuestions returns pending questions not yet answered.
func (s *Store) GetUnresolvedQuestions(ctx context.Context, limit int) ([]PendingQuestion, error) {
	return s.GetUnresolvedPendingQuestions(ctx, limit)
}

// GetRecentlyResolvedQuestions returns pending questions resolved after since.
func (s *Store) GetRecentlyResolvedQuestions(ctx context.Context, since time.Time) ([]PendingQuestion, error) {
	return s.GetRecentlyResolvedPendingQuestions(ctx, since)
}

// ResolveQuestion marks a pending question as answered.
func (s *Store) ResolveQuestion(ctx context.Context, uuid, answer string) error {
	return s.ResolvePendingQuestion(ctx, uuid, answer)
}


// BackfillEmbeddings generates missing embeddings for up to limit knowledge nodes.
func (s *Store) BackfillEmbeddings(ctx context.Context, limit int) (int, error) {
	return s.BackfillEntryEmbeddings(ctx, limit)
}

// Entries returns the EntryStore view of this Store.
func (s *Store) Entries() EntryStore { return s }

// Knowledge returns the KnowledgeStore view of this Store.
func (s *Store) Knowledge() KnowledgeStore { return s }

// Graph returns the GraphStore view of this Store.
func (s *Store) Graph() GraphStore { return s }

// Tasks returns the TaskStore view of this Store.
func (s *Store) Tasks() TaskStore { return s }

// Agent returns the AgentOps view of this Store.
func (s *Store) Agent() AgentOps { return s }

// Admin returns the AdminOps view of this Store.
func (s *Store) Admin() AdminOps { return s }
