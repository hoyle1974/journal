package memory

import (
	"context"
	"fmt"
)

// GraphExpandResult contains the seed node plus its immediate graph neighbourhood:
// outgoing SPO edges (object_uuid == seedID), incoming entity_link edges (entity_links contains seedID),
// and directly entity-linked nodes (entity_links of the seed node itself).
type GraphExpandResult struct {
	Seed     *KnowledgeNodeWithLinks
	Outgoing []KnowledgeNode // nodes where object_uuid == seedID
	Incoming []KnowledgeNode // nodes where entity_links contains seedID
	Linked   []KnowledgeNode // nodes directly referenced by seed.EntityLinks
}

// GraphExpand fetches the seed node and its 1-hop neighbourhood from the knowledge graph.
// It returns:
//   - Seed: the full node (with entity_links) identified by seedID
//   - Outgoing: relational nodes where this node is the subject (object_uuid == seedID)
//   - Incoming: nodes that list seedID in their entity_links
//   - Linked: nodes directly referenced by the seed's own entity_links list
//
// hops is reserved for future multi-hop traversal; currently only 1-hop is supported.
// limitPerEdge caps each of the three neighbour sets.
func (s *Store) GraphExpand(ctx context.Context, seedID string, hops, limitPerEdge int) (*GraphExpandResult, error) {
	if seedID == "" {
		return nil, fmt.Errorf("seedID required")
	}

	if limitPerEdge <= 0 {
		limitPerEdge = 10
	}

	// Fetch the seed node with its entity_links.
	// TODO(batch-2): GetKnowledgeNodeByID will be converted to a Store method; pass nil env for now.
	seed, err := GetKnowledgeNodeByID(ctx, nil, seedID)
	if err != nil {
		return nil, fmt.Errorf("fetch seed node: %w", err)
	}

	// Outgoing edges: nodes where object_uuid == seedID (seed is the subject of an SPO triple).
	// TODO(batch-2): QueryOutgoingEdges will be converted to a Store method; pass nil env for now.
	outgoing, err := QueryOutgoingEdges(ctx, nil, seedID, limitPerEdge)
	if err != nil {
		return nil, fmt.Errorf("query outgoing edges: %w", err)
	}

	// Incoming edges: nodes that reference seedID in their entity_links array.
	// TODO(batch-2): QueryNodesLinkingTo will be converted to a Store method; pass nil env for now.
	incoming, err := QueryNodesLinkingTo(ctx, nil, seedID, limitPerEdge)
	if err != nil {
		return nil, fmt.Errorf("query incoming edges: %w", err)
	}

	// Linked nodes: fetch the nodes pointed to by seed.EntityLinks.
	var linked []KnowledgeNode
	if len(seed.EntityLinks) > 0 {
		ids := seed.EntityLinks
		if len(ids) > limitPerEdge {
			ids = ids[:limitPerEdge]
		}
		// TODO(batch-2): GetKnowledgeNodesByIDs will be converted to a Store method; pass nil env for now.
		linked, err = GetKnowledgeNodesByIDs(ctx, nil, ids)
		if err != nil {
			return nil, fmt.Errorf("fetch linked nodes: %w", err)
		}
	}
	if outgoing == nil {
		outgoing = []KnowledgeNode{}
	}
	if incoming == nil {
		incoming = []KnowledgeNode{}
	}
	if linked == nil {
		linked = []KnowledgeNode{}
	}

	s.log.Info("graph expand complete",
		"seed_id", seedID,
		"outgoing", len(outgoing),
		"incoming", len(incoming),
		"linked", len(linked),
	)

	return &GraphExpandResult{
		Seed:     seed,
		Outgoing: outgoing,
		Incoming: incoming,
		Linked:   linked,
	}, nil
}
