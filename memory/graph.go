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
	Nodes    map[string]KnowledgeNodeWithLinks
	Edges    []Edge
	SeedUUIDs map[string]bool // UUIDs of the initial keyword-matched seed nodes
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
	sg.writeEntitiesAndRelationships(&sb)
	return strings.TrimRight(sb.String(), "\n")
}

// ToMarkdownFull renders the full normalized SubGraph as Markdown without a
// per-seed header. Use this when the graph was built from multiple seeds.
func (sg *SubGraph) ToMarkdownFull() string {
	var sb strings.Builder
	sb.WriteString("# Knowledge Graph\n\n")
	sg.writeEntitiesAndRelationships(&sb)
	return strings.TrimRight(sb.String(), "\n")
}

// ToDOT serializes the SubGraph as a Graphviz DOT string, rendering only the
// SPO relationships (matching the ## Relationships section of ToMarkdownFull).
// Each relationship node becomes a labeled edge between its subject and object.
// Seed subject/object nodes are highlighted.
func (sg *SubGraph) ToDOT() string {
	var sb strings.Builder
	sb.WriteString("digraph knowledge {\n")
	sb.WriteString("  graph [bgcolor=\"#1a1a2e\" fontname=\"Helvetica\" rankdir=LR];\n")
	sb.WriteString("  node  [shape=box style=\"filled,rounded\" fontname=\"Helvetica\" fontsize=11 fontcolor=\"white\" color=\"#4a90d9\" fillcolor=\"#2d2d44\"];\n")
	sb.WriteString("  edge  [fontname=\"Helvetica\" fontsize=9 color=\"#888888\" fontcolor=\"#aaaaaa\"];\n")

	// Collect entity nodes that appear as subject or object in a relationship.
	referenced := make(map[string]bool)
	for _, n := range sg.Nodes {
		if n.NodeType != NodeTypeRelationship {
			continue
		}
		if n.SubjectUUID != "" {
			referenced[n.SubjectUUID] = true
		}
		if n.ObjectUUID != "" {
			referenced[n.ObjectUUID] = true
		}
	}

	// Emit only the entity nodes that participate in at least one relationship.
	for uuid := range referenced {
		n, ok := sg.Nodes[uuid]
		if !ok {
			continue
		}
		label := nodeLabel(n)
		label = strings.ReplaceAll(label, `"`, `\"`)
		attrs := fmt.Sprintf(`label="%s"`, label)
		if sg.SeedUUIDs[uuid] {
			attrs += ` fillcolor="#c0392b" color="#e74c3c"`
		}
		fmt.Fprintf(&sb, "  %q [%s];\n", uuid, attrs)
	}

	// Emit one edge per relationship node (subject → object, labeled with predicate).
	seen := make(map[string]bool)
	for _, n := range sg.Nodes {
		if n.NodeType != NodeTypeRelationship || n.SubjectUUID == "" || n.ObjectUUID == "" {
			continue
		}
		key := n.SubjectUUID + "|" + n.Predicate + "|" + n.ObjectUUID
		if seen[key] {
			continue
		}
		seen[key] = true
		pred := strings.ReplaceAll(n.Predicate, `"`, `\"`)
		edgeAttrs := fmt.Sprintf("label=%q", pred)
		if sg.SeedUUIDs[n.UUID] {
			edgeAttrs += ` color="#e74c3c" fontcolor="#e74c3c"`
		}
		fmt.Fprintf(&sb, "  %q -> %q [%s];\n", n.SubjectUUID, n.ObjectUUID, edgeAttrs)
	}

	sb.WriteString("}\n")
	return sb.String()
}

// noisyNodeTypes are node types excluded from rendered graph output because they
// add structural noise without semantic value (audit responses, raw log entries).
var noisyNodeTypes = map[string]bool{
	"response": true,
	"log":      true,
}

// titleCase returns s with the first byte uppercased (ASCII node type names only).
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// nodeLabel returns a short display label for a node: Type[Content].
func nodeLabel(n KnowledgeNodeWithLinks) string {
	content := n.Content
	if len(content) > 60 {
		content = content[:57] + "..."
	}
	return titleCase(n.NodeType) + "[" + content + "]"
}

func (sg *SubGraph) writeEntitiesAndRelationships(sb *strings.Builder) {
	sb.WriteString("## Entities\n")
	for uuid, n := range sg.Nodes {
		if noisyNodeTypes[n.NodeType] || n.NodeType == NodeTypeRelationship {
			continue
		}
		label := nodeLabel(n)
		if n.SignificanceWeight > 0 {
			label += fmt.Sprintf(" (sig:%.1f)", n.SignificanceWeight)
		}
		if sg.SeedUUIDs[uuid] {
			label += " ★"
		}
		fmt.Fprintf(sb, "* %s\n", label)
	}

	sb.WriteString("\n## Relationships\n")
	seenRel := make(map[string]bool)
	for uuid, n := range sg.Nodes {
		if n.NodeType != NodeTypeRelationship {
			continue
		}
		key := n.SubjectUUID + "|" + n.Predicate + "|" + n.ObjectUUID
		if seenRel[key] {
			continue
		}
		seenRel[key] = true
		subj, subjOK := sg.Nodes[n.SubjectUUID]
		obj, objOK := sg.Nodes[n.ObjectUUID]
		subjLabel := n.SubjectUUID
		if subjOK {
			subjLabel = nodeLabel(subj)
		}
		objLabel := n.ObjectUUID
		if objOK {
			objLabel = nodeLabel(obj)
		}
		line := fmt.Sprintf("* %s -> %s -> %s", subjLabel, n.Predicate, objLabel)
		if sg.SeedUUIDs[uuid] {
			line += " ★"
		}
		fmt.Fprintln(sb, line)
	}

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

// GraphExpand performs a BFS graph traversal starting from a single seedID.
// It is a convenience wrapper around GraphExpandMulti.
func (s *Store) GraphExpand(ctx context.Context, seedID string, queryVector []float32, hops, limitPerEdge int) (*SubGraph, error) {
	if seedID == "" {
		return nil, fmt.Errorf("seedID required")
	}
	return s.GraphExpandMulti(ctx, []string{seedID}, queryVector, hops, limitPerEdge)
}

// GraphExpandMulti performs a BFS graph traversal from multiple seed nodes and
// returns a single normalized SubGraph. All seed neighborhoods are merged into
// one deduplicated result: nodes are keyed by UUID (no duplicates), edges are
// deduplicated, and every node referenced by an edge is resolved so that names
// appear instead of raw IDs in the output.
//
// queryVector is used for semantic pruning at each hop (top-K by cosine similarity).
// If queryVector is nil, a hard cap of limitPerEdge is applied instead.
// hops controls the traversal depth (1 = immediate neighbourhood only).
func (s *Store) GraphExpandMulti(ctx context.Context, seedIDs []string, queryVector []float32, hops, limitPerEdge int) (*SubGraph, error) {
	if len(seedIDs) == 0 {
		return nil, fmt.Errorf("seedIDs required")
	}
	if limitPerEdge <= 0 {
		limitPerEdge = 10
	}
	if hops <= 0 {
		hops = 1
	}

	seedSet := make(map[string]bool, len(seedIDs))
	for _, id := range seedIDs {
		seedSet[id] = true
	}

	sg := &SubGraph{
		Nodes:     make(map[string]KnowledgeNodeWithLinks),
		Edges:     make([]Edge, 0),
		SeedUUIDs: seedSet,
	}

	// Batch-fetch all seed nodes (GetKnowledgeNodesByIDs returns EntityLinks).
	seeds, err := s.GetKnowledgeNodesByIDs(ctx, seedIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch seed nodes: %w", err)
	}
	for _, n := range seeds {
		sg.Nodes[n.UUID] = n
	}

	visited := make(map[string]bool, len(seedIDs))
	for _, id := range seedIDs {
		visited[id] = true
	}
	currentHop := make([]string, len(seedIDs))
	copy(currentHop, seedIDs)

	var mu sync.Mutex // protects sg.Edges, sg.Nodes, visited, nextCandidateUUIDs

	for hop := 0; hop < hops && len(currentHop) > 0; hop++ {
		// For hop > 0, batch-fetch frontier nodes not yet in sg.Nodes.
		if hop > 0 {
			toFetch := make([]string, 0, len(currentHop))
			for _, id := range currentHop {
				if _, ok := sg.Nodes[id]; !ok {
					toFetch = append(toFetch, id)
				}
			}
			if len(toFetch) > 0 {
				nodes, err := s.GetKnowledgeNodesByIDs(ctx, toFetch)
				if err != nil {
					return nil, fmt.Errorf("hop %d batch fetch: %w", hop, err)
				}
				for _, n := range nodes {
					sg.Nodes[n.UUID] = n
				}
			}
		}

		nextCandidateUUIDs := make(map[string]bool)

		g, gctx := errgroup.WithContext(ctx)
		for _, nodeUUID := range currentHop {
			nodeUUID := nodeUUID // capture
			g.Go(func() error {
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

				// Build local edge and new-UUID slices without holding the lock.
				localEdges := make([]Edge, 0, 1+len(incomingSPO)+len(incoming)+min(len(entityLinks), limitPerEdge))
				newUUIDs := make([]string, 0, cap(localEdges))

				if node.ObjectUUID != "" {
					localEdges = append(localEdges, Edge{SourceUUID: nodeUUID, TargetUUID: node.ObjectUUID, Predicate: node.Predicate})
					newUUIDs = append(newUUIDs, node.ObjectUUID)
				}
				// For relationship nodes, also traverse the subject so that the full
				// SPO triple is reachable even when the seed is the relationship itself.
				if node.SubjectUUID != "" {
					newUUIDs = append(newUUIDs, node.SubjectUUID)
				}
				for _, n := range incomingSPO {
					localEdges = append(localEdges, Edge{SourceUUID: n.UUID, TargetUUID: nodeUUID, Predicate: n.Predicate})
					newUUIDs = append(newUUIDs, n.UUID)
				}
				for _, n := range incoming {
					localEdges = append(localEdges, Edge{SourceUUID: n.UUID, TargetUUID: nodeUUID, Predicate: "incoming_link"})
					newUUIDs = append(newUUIDs, n.UUID)
				}
				linkCap := min(len(entityLinks), limitPerEdge)
				for _, linkedUUID := range entityLinks[:linkCap] {
					localEdges = append(localEdges, Edge{SourceUUID: nodeUUID, TargetUUID: linkedUUID, Predicate: "entity_link"})
					newUUIDs = append(newUUIDs, linkedUUID)
				}
				// Also mark all entity_links visited regardless of cap (they exist as nodes).
				for _, id := range entityLinks {
					newUUIDs = append(newUUIDs, id)
				}

				// Single brief lock acquisition to merge into shared state.
				mu.Lock()
				defer mu.Unlock()
				sg.Edges = append(sg.Edges, localEdges...)
				for _, id := range newUUIDs {
					if !visited[id] {
						nextCandidateUUIDs[id] = true
						visited[id] = true
					}
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
		// Cache pruned nodes now so the hop-start fetch skips them entirely.
		for _, n := range pruned {
			sg.Nodes[n.UUID] = n
		}
		currentHop = make([]string, 0, len(pruned))
		for _, n := range pruned {
			currentHop = append(currentHop, n.UUID)
		}
	}

	// Resolve all nodes referenced in edges or relationship-node SPO fields but not yet fetched.
	// Loop until no new nodes are discovered — each pass may fetch relationship nodes whose
	// SubjectUUID/ObjectUUID reveal further unresolved entity nodes.
	resolveDangling := func() bool {
		danglingIDs := make(map[string]bool)
		for _, e := range sg.Edges {
			if _, ok := sg.Nodes[e.SourceUUID]; !ok {
				danglingIDs[e.SourceUUID] = true
			}
			if _, ok := sg.Nodes[e.TargetUUID]; !ok {
				danglingIDs[e.TargetUUID] = true
			}
		}
		for _, n := range sg.Nodes {
			if n.NodeType != NodeTypeRelationship {
				continue
			}
			if n.SubjectUUID != "" {
				if _, ok := sg.Nodes[n.SubjectUUID]; !ok {
					danglingIDs[n.SubjectUUID] = true
				}
			}
			if n.ObjectUUID != "" {
				if _, ok := sg.Nodes[n.ObjectUUID]; !ok {
					danglingIDs[n.ObjectUUID] = true
				}
			}
		}
		if len(danglingIDs) == 0 {
			return false
		}
		ids := make([]string, 0, len(danglingIDs))
		for id := range danglingIDs {
			ids = append(ids, id)
		}
		resolved, err := s.GetKnowledgeNodesByIDs(ctx, ids)
		if err != nil {
			s.log.Debug("graph expand: failed to resolve dangling nodes", "error", err)
			return false
		}
		for _, n := range resolved {
			sg.Nodes[n.UUID] = n
		}
		return true
	}
	const maxResolvePasses = 4
	for range maxResolvePasses {
		if !resolveDangling() {
			break
		}
	}

	// Deduplicate edges accumulated across all seed BFS paths.
	sg.Edges = deduplicateEdges(sg.Edges)

	s.log.Info("graph expand complete",
		"seeds", len(seedIDs),
		"hops", hops,
		"nodes", len(sg.Nodes),
		"edges", len(sg.Edges),
	)
	return sg, nil
}

// deduplicateEdges removes duplicate edges, keeping the first occurrence of
// each (sourceUUID, predicate, targetUUID) triple.
func deduplicateEdges(edges []Edge) []Edge {
	seen := make(map[string]bool, len(edges))
	result := make([]Edge, 0, len(edges))
	for _, e := range edges {
		key := e.SourceUUID + "|" + e.Predicate + "|" + e.TargetUUID
		if !seen[key] {
			seen[key] = true
			result = append(result, e)
		}
	}
	return result
}
