package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

// Edge represents a directed relationship between two knowledge nodes.
type Edge struct {
	SourceUUID string
	TargetUUID string
	// Predicate is the relationship label. For QueryIncomingSPOEdges results it is
	// the SPO predicate from the node. For QueryNodesLinkingTo results it is
	// "incoming_link". For EntityLinks it is "entity_link". For a node's own
	// ObjectUUID (intrinsic outgoing SPO edge) it is the node's own Predicate.
	Predicate string
}

// SubGraph is the result of a graph traversal. Nodes is keyed by UUID.
// KnowledgeNodeWithLinks is used so EntityLinks are available at every BFS hop.
type SubGraph struct {
	Nodes map[string]KnowledgeNodeWithLinks
	Edges []Edge
}

// ToMarkdown serializes the SubGraph as Markdown optimized for LLM context injection.
// seedID identifies the traversal origin for the header line.
func (sg *SubGraph) ToMarkdown(seedID string) string {
	var sb strings.Builder
	seedContent := seedID
	if seed, ok := sg.Nodes[seedID]; ok {
		seedContent = seed.Content
	}
	sb.WriteString("# Knowledge Graph Neighborhood\n")
	fmt.Fprintf(&sb, "**Seed Concept:** %q (ID: %s)\n\n", seedContent, seedID)

	sb.WriteString("## Entities\n")
	for uuid, n := range sg.Nodes {
		content := n.Content
		if len(content) > 120 {
			content = content[:117] + "..."
		}
		fmt.Fprintf(&sb, "* [%s] %s: %q\n", uuid, n.NodeType, content)
	}

	if len(sg.Edges) > 0 {
		sb.WriteString("\n## Relationships\n")
		for _, e := range sg.Edges {
			srcContent := e.SourceUUID
			if n, ok := sg.Nodes[e.SourceUUID]; ok {
				srcContent = truncateString(n.Content, 40)
			}
			tgtContent := e.TargetUUID
			if n, ok := sg.Nodes[e.TargetUUID]; ok {
				tgtContent = truncateString(n.Content, 40)
			}
			fmt.Fprintf(&sb, "* [%s] %s -> %s -> [%s] %s\n",
				e.SourceUUID, srcContent, e.Predicate, e.TargetUUID, tgtContent)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// pruneCandidates returns the top-maxK candidates sorted by cosine similarity to
// queryVector. If queryVector is nil or any candidate lacks an Embedding, returns
// the first maxK candidates unchanged (hard cap).
func (s *Store) pruneCandidates(candidates []KnowledgeNodeWithLinks, queryVector []float32, maxK int) []KnowledgeNodeWithLinks {
	if len(candidates) <= maxK {
		return candidates
	}
	if maxK <= 0 {
		return nil
	}
	if len(queryVector) == 0 {
		return candidates[:maxK]
	}
	// Check all candidates have embeddings.
	for _, c := range candidates {
		if len(c.Embedding) == 0 {
			return candidates[:maxK]
		}
	}
	// Sort by cosine similarity descending.
	type scoredNode struct {
		node  KnowledgeNodeWithLinks
		score float64
	}
	scoredNodes := make([]scoredNode, len(candidates))
	for i, c := range candidates {
		scoredNodes[i].node = c
		scoredNodes[i].score = cosineSimilarity(queryVector, c.Embedding)
	}
	sort.Slice(scoredNodes, func(i, j int) bool {
		return scoredNodes[i].score > scoredNodes[j].score
	})
	result := make([]KnowledgeNodeWithLinks, maxK)
	for i := 0; i < maxK; i++ {
		result[i] = scoredNodes[i].node
	}
	return result
}

// GraphExpand performs a BFS graph traversal starting from seedID.
// queryVector is used for semantic pruning at each hop (top-K by cosine similarity).
// If queryVector is nil, a hard cap of limitPerEdge is applied instead.
// hops controls the traversal depth (1 = immediate neighbourhood only).
// limitPerEdge caps results per Firestore query per node, and also caps the
// inter-hop frontier (limitPerHop = limitPerEdge).
func (s *Store) GraphExpand(ctx context.Context, seedID string, queryVector []float32, hops, limitPerEdge int) (*SubGraph, error) {
	if seedID == "" {
		return nil, fmt.Errorf("seedID required")
	}
	if limitPerEdge <= 0 {
		limitPerEdge = 10
	}
	if hops <= 0 {
		hops = 1
	}

	sg := &SubGraph{
		Nodes: make(map[string]KnowledgeNodeWithLinks),
		Edges: make([]Edge, 0),
	}

	// Seed fetch — uses GetKnowledgeNodeByID to obtain EntityLinks.
	seed, err := s.GetKnowledgeNodeByID(ctx, seedID)
	if err != nil {
		return nil, fmt.Errorf("fetch seed node: %w", err)
	}
	sg.Nodes[seedID] = *seed

	visited := map[string]bool{seedID: true}
	currentHop := []string{seedID}

	var mu sync.Mutex // protects sg.Edges and nextCandidateUUIDs

	for hop := 0; hop < hops && len(currentHop) > 0; hop++ {
		// For hop > 0, batch-fetch the current frontier nodes.
		if hop > 0 {
			nodes, err := s.GetKnowledgeNodesByIDs(ctx, currentHop)
			if err != nil {
				return nil, fmt.Errorf("hop %d batch fetch: %w", hop, err)
			}
			mu.Lock()
			for _, n := range nodes {
				sg.Nodes[n.UUID] = n
			}
			mu.Unlock()
		}

		nextCandidateUUIDs := make(map[string]bool)

		g, gctx := errgroup.WithContext(ctx)
		for _, nodeUUID := range currentHop {
			nodeUUID := nodeUUID // capture
			g.Go(func() error {
				// Read entity links from the already-stored node.
				mu.Lock()
				node := sg.Nodes[nodeUUID]
				mu.Unlock()
				entityLinks := node.EntityLinks

				incomingSPO, err := s.QueryIncomingSPOEdges(gctx, nodeUUID, limitPerEdge)
				if err != nil {
					s.log.Debug("graph expand: incoming SPO edges error", "uuid", nodeUUID, "error", err)
					incomingSPO = nil
				}
				incoming, err := s.QueryNodesLinkingTo(gctx, nodeUUID, limitPerEdge)
				if err != nil {
					s.log.Debug("graph expand: incoming edges error", "uuid", nodeUUID, "error", err)
					incoming = nil
				}

				mu.Lock()
				defer mu.Unlock()

				// Traverse the node's intrinsic outgoing SPO edge (this node as subject).
				if node.ObjectUUID != "" {
					sg.Edges = append(sg.Edges, Edge{SourceUUID: nodeUUID, TargetUUID: node.ObjectUUID, Predicate: node.Predicate})
					if !visited[node.ObjectUUID] {
						nextCandidateUUIDs[node.ObjectUUID] = true
					}
				}
				for _, n := range incomingSPO {
					sg.Edges = append(sg.Edges, Edge{SourceUUID: n.UUID, TargetUUID: nodeUUID, Predicate: n.Predicate})
					if !visited[n.UUID] {
						nextCandidateUUIDs[n.UUID] = true
					}
				}
				for _, n := range incoming {
					sg.Edges = append(sg.Edges, Edge{SourceUUID: n.UUID, TargetUUID: nodeUUID, Predicate: "incoming_link"})
					if !visited[n.UUID] {
						nextCandidateUUIDs[n.UUID] = true
					}
				}
				cap := len(entityLinks)
				if cap > limitPerEdge {
					cap = limitPerEdge
				}
				for _, linkedUUID := range entityLinks[:cap] {
					sg.Edges = append(sg.Edges, Edge{SourceUUID: nodeUUID, TargetUUID: linkedUUID, Predicate: "entity_link"})
					if !visited[linkedUUID] {
						nextCandidateUUIDs[linkedUUID] = true
					}
				}
				// Mark all discovered UUIDs visited before next hop.
				if node.ObjectUUID != "" {
					visited[node.ObjectUUID] = true
				}
				for _, n := range incomingSPO {
					visited[n.UUID] = true
				}
				for _, n := range incoming {
					visited[n.UUID] = true
				}
				for _, id := range entityLinks {
					visited[id] = true
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, fmt.Errorf("hop %d traversal: %w", hop, err)
		}

		// Collect candidate UUIDs and batch-fetch for pruning.
		candidateIDs := make([]string, 0, len(nextCandidateUUIDs))
		for id := range nextCandidateUUIDs {
			candidateIDs = append(candidateIDs, id)
		}
		if len(candidateIDs) == 0 {
			break
		}
		candidateNodes, err := s.GetKnowledgeNodesByIDs(ctx, candidateIDs)
		if err != nil {
			return nil, fmt.Errorf("hop %d candidate fetch: %w", hop, err)
		}
		pruned := s.pruneCandidates(candidateNodes, queryVector, limitPerEdge)
		currentHop = make([]string, 0, len(pruned))
		for _, n := range pruned {
			currentHop = append(currentHop, n.UUID)
		}
	}

	s.log.Info("graph expand complete",
		"seed_id", seedID,
		"hops", hops,
		"nodes", len(sg.Nodes),
		"edges", len(sg.Edges),
	)
	return sg, nil
}
