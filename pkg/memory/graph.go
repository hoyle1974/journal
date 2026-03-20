package memory

import (
	"context"
	"fmt"

	"github.com/jackstrohm/jot/internal/infra"
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
func GraphExpand(ctx context.Context, env infra.ToolEnv, seedID string, hops, limitPerEdge int) (*GraphExpandResult, error) {
	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	if seedID == "" {
		return nil, fmt.Errorf("seedID required")
	}

	ctx, span := infra.StartSpan(ctx, "memory.graph_expand")
	defer span.End()
	span.SetAttributes(map[string]string{"seed_id": seedID, "hops": fmt.Sprintf("%d", hops)})
	if limitPerEdge <= 0 {
		limitPerEdge = 10
	}

	// Fetch the seed node with its entity_links.
	seed, err := GetKnowledgeNodeByID(ctx, env, seedID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("fetch seed node: %w", err)
	}

	// Outgoing edges: nodes where object_uuid == seedID (seed is the subject of an SPO triple).
	outgoing, err := QueryOutgoingEdges(ctx, env, seedID, limitPerEdge)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("query outgoing edges: %w", err)
	}

	// Incoming edges: nodes that reference seedID in their entity_links array.
	incoming, err := QueryNodesLinkingTo(ctx, env, seedID, limitPerEdge)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("query incoming edges: %w", err)
	}

	// Linked nodes: fetch the nodes pointed to by seed.EntityLinks.
	var linked []KnowledgeNode
	if len(seed.EntityLinks) > 0 {
		ids := seed.EntityLinks
		if len(ids) > limitPerEdge {
			ids = ids[:limitPerEdge]
		}
		linked, err = GetKnowledgeNodesByIDs(ctx, env, ids)
		if err != nil {
			span.RecordError(err)
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

	infra.LoggerFrom(ctx).Info("graph expand complete",
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
