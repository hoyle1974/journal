package memory

import (
	"math"
	"testing"
	"time"
)

func TestFuseKnowledgeNodes_SetsQueryScore(t *testing.T) {
	vecNodes := []KnowledgeNode{
		{UUID: "a", Content: "alpha", QueryScore: 0.9},
		{UUID: "b", Content: "beta", QueryScore: 0.8},
	}
	kwNodes := []KnowledgeNode{
		{UUID: "b", Content: "beta", QueryScore: 0.8},
		{UUID: "c", Content: "gamma", QueryScore: 0.7},
	}
	result := FuseKnowledgeNodes(vecNodes, kwNodes, 3)
	for _, n := range result {
		if n.QueryScore <= 0 {
			t.Errorf("node %s has zero QueryScore after fusion", n.UUID)
		}
	}
	// "b" appears in both lists so must have higher RRF score than "a" or "c"
	var scoreB, scoreA float64
	for _, n := range result {
		switch n.UUID {
		case "a":
			scoreA = n.QueryScore
		case "b":
			scoreB = n.QueryScore
		}
	}
	if scoreB <= scoreA {
		t.Errorf("node 'b' (in both lists) should outscore 'a' (vector-only): b=%f a=%f", scoreB, scoreA)
	}
}

func TestApplyTemporalBias_FreshNodeUnchanged(t *testing.T) {
	nodes := []KnowledgeNode{
		{UUID: "fresh", QueryScore: 1.0, Timestamp: time.Now().Format(time.RFC3339)},
	}
	result := ApplyTemporalBias(nodes, 180)
	// e^(-λ*0) ≈ 1.0, so score should be essentially unchanged
	if result[0].QueryScore < 0.99 {
		t.Errorf("fresh node score should be ~1.0, got %f", result[0].QueryScore)
	}
}

func TestApplyTemporalBias_OldNodeDecays(t *testing.T) {
	oldTS := time.Now().AddDate(-2, 0, 0).Format(time.RFC3339) // 2 years ago
	nodes := []KnowledgeNode{
		{UUID: "old", QueryScore: 1.0, Timestamp: oldTS},
	}
	result := ApplyTemporalBias(nodes, 180)
	// 2 years = ~730 days; λ = ln(2)/180 ≈ 0.00385; decay = e^(-0.00385*730) ≈ 0.057
	if result[0].QueryScore > 0.10 {
		t.Errorf("2-year-old node should have heavily decayed score, got %f", result[0].QueryScore)
	}
}

func TestApplyTemporalBias_HalfLifeCorrect(t *testing.T) {
	halfLifeDays := 180.0
	ts := time.Now().Add(-time.Duration(halfLifeDays * 24 * float64(time.Hour))).Format(time.RFC3339)
	nodes := []KnowledgeNode{
		{UUID: "halflife", QueryScore: 1.0, Timestamp: ts},
	}
	result := ApplyTemporalBias(nodes, halfLifeDays)
	// After exactly one half-life, score should be ~0.5
	if math.Abs(result[0].QueryScore-0.5) > 0.05 {
		t.Errorf("at half-life, score should be ~0.5, got %f", result[0].QueryScore)
	}
}

func TestApplyTemporalBias_SortsNewerFirst(t *testing.T) {
	oldTS := time.Now().AddDate(-1, 0, 0).Format(time.RFC3339)
	newTS := time.Now().Format(time.RFC3339)
	nodes := []KnowledgeNode{
		{UUID: "old", QueryScore: 0.95, Timestamp: oldTS},
		{UUID: "new", QueryScore: 0.80, Timestamp: newTS},
	}
	result := ApplyTemporalBias(nodes, 180)
	if result[0].UUID != "new" {
		t.Errorf("expected fresh node to be ranked first, got %s", result[0].UUID)
	}
}

func TestApplyTemporalBias_MissingTimestampNoDecay(t *testing.T) {
	nodes := []KnowledgeNode{
		{UUID: "notimestamp", QueryScore: 0.8, Timestamp: ""},
	}
	result := ApplyTemporalBias(nodes, 180)
	if result[0].QueryScore != 0.8 {
		t.Errorf("node with missing timestamp should keep original score, got %f", result[0].QueryScore)
	}
}
