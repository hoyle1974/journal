package memory

import (
	"sort"
)

const rrfK = 60

// FuseKnowledgeNodes combines vector and keyword search results using Reciprocal Rank Fusion.
func FuseKnowledgeNodes(vectorNodes []KnowledgeNode, keywordNodes []KnowledgeNode, topN int) []KnowledgeNode {
	type scored struct {
		node  KnowledgeNode
		score float64
	}
	scores := make(map[string]*scored)
	for rank, n := range vectorNodes {
		if n.UUID == "" {
			continue
		}
		if s, ok := scores[n.UUID]; ok {
			s.score += 1.0 / (float64(rrfK) + float64(rank+1))
		} else {
			scores[n.UUID] = &scored{node: n, score: 1.0 / (float64(rrfK) + float64(rank+1))}
		}
	}
	for rank, n := range keywordNodes {
		if n.UUID == "" {
			continue
		}
		if s, ok := scores[n.UUID]; ok {
			s.score += 1.0 / (float64(rrfK) + float64(rank+1))
		} else {
			scores[n.UUID] = &scored{node: n, score: 1.0 / (float64(rrfK) + float64(rank+1))}
		}
	}
	var out []scored
	for _, s := range scores {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].score > out[j].score })
	if topN <= 0 || len(out) == 0 {
		return nil
	}
	if topN > len(out) {
		topN = len(out)
	}
	result := make([]KnowledgeNode, 0, topN)
	for i := 0; i < topN; i++ {
		result = append(result, out[i].node)
	}
	return result
}
