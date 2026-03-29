package memory

import (
	"math"
	"sort"
	"time"
)

const rrfK = 60

// TemporalDecayHalfLifeDays is the default half-life for temporal bias scoring.
// A node's score is halved for every 180 days since its timestamp.
const TemporalDecayHalfLifeDays = 180.0

// FuseKnowledgeNodes combines vector and keyword search results using Reciprocal Rank Fusion.
// The RRF score is written back to each node's QueryScore so that downstream callers
// (e.g. ApplyTemporalBias) have a meaningful numeric score to operate on.
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
		s := 1.0 / (float64(rrfK) + float64(rank+1))
		if existing, ok := scores[n.UUID]; ok {
			existing.score += s
		} else {
			scores[n.UUID] = &scored{node: n, score: s}
		}
	}
	for rank, n := range keywordNodes {
		if n.UUID == "" {
			continue
		}
		s := 1.0 / (float64(rrfK) + float64(rank+1))
		if existing, ok := scores[n.UUID]; ok {
			existing.score += s
		} else {
			scores[n.UUID] = &scored{node: n, score: s}
		}
	}
	out := make([]scored, 0, len(scores))
	for _, s := range scores {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].score > out[j].score })
	if len(out) == 0 {
		return nil
	}
	if topN <= 0 {
		topN = len(out)
	}
	if topN > len(out) {
		topN = len(out)
	}
	result := make([]KnowledgeNode, 0, topN)
	for i := 0; i < topN; i++ {
		n := out[i].node
		n.QueryScore = out[i].score // propagate RRF score
		result = append(result, n)
	}
	return result
}

// ApplyTemporalBias applies exponential decay to each node's QueryScore based on the
// node's Timestamp and returns the nodes re-sorted by the decayed score (highest first).
//
// The decay formula is: decayedScore = QueryScore × e^(-λ × ageDays)
// where λ = ln(2) / halfLifeDays, giving a half-life of halfLifeDays days.
//
// Nodes with an empty or unparseable Timestamp are left at their original QueryScore.
// halfLifeDays must be > 0; if <= 0, TemporalDecayHalfLifeDays is used.
func ApplyTemporalBias(nodes []KnowledgeNode, halfLifeDays float64) []KnowledgeNode {
	if halfLifeDays <= 0 {
		halfLifeDays = TemporalDecayHalfLifeDays
	}
	lambda := math.Log(2) / halfLifeDays
	now := time.Now()

	result := make([]KnowledgeNode, len(nodes))
	copy(result, nodes)

	for i, n := range result {
		if n.Timestamp == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, n.Timestamp)
		if err != nil {
			continue
		}
		ageDays := now.Sub(ts).Hours() / 24
		if ageDays < 0 {
			ageDays = 0
		}
		result[i].QueryScore = n.QueryScore * math.Exp(-lambda*ageDays)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].QueryScore > result[j].QueryScore
	})
	return result
}
