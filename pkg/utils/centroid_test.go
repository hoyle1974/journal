package utils

import (
	"math"
	"testing"
)

func approxEqualVec(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) >= 1e-5 {
			return false
		}
	}
	return true
}

func TestMeanVector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		vecs [][]float32
		want []float32 // nil means expect nil result
	}{
		{name: "nil input", vecs: nil, want: nil},
		{name: "empty slice", vecs: [][]float32{}, want: nil},
		{name: "zero-dimension vector", vecs: [][]float32{{}}, want: nil},
		{
			name: "inconsistent dimensions",
			vecs: [][]float32{{1, 2}, {1, 2, 3}},
			want: nil,
		},
		{
			name: "single vector",
			vecs: [][]float32{{3, 1, 4}},
			want: []float32{3, 1, 4},
		},
		{
			name: "two identical vectors",
			vecs: [][]float32{{1, 2, 3}, {1, 2, 3}},
			want: []float32{1, 2, 3},
		},
		{
			name: "two opposite vectors",
			vecs: [][]float32{{1, 0}, {-1, 0}},
			want: []float32{0, 0},
		},
		{
			name: "three vectors element-wise mean",
			vecs: [][]float32{{1, 0}, {0, 1}, {0, 0}},
			want: []float32{1.0 / 3.0, 1.0 / 3.0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MeanVector(tc.vecs)
			if tc.want == nil {
				if got != nil {
					t.Errorf("MeanVector() = %v, want nil", got)
				}
				return
			}
			if !approxEqualVec(got, tc.want) {
				t.Errorf("MeanVector() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClusterByRadius(t *testing.T) {
	t.Parallel()

	node := func(id string, emb []float32) ClusterableNode {
		return ClusterableNode{ID: id, Embedding: emb}
	}

	tests := []struct {
		name         string
		nodes        []ClusterableNode
		threshold    float64
		clusterSizes []int // expected member count per cluster (order-sensitive); nil means zero clusters
	}{
		{
			name:      "empty nodes",
			nodes:     []ClusterableNode{},
			threshold: 0.9,
		},
		{
			name:      "all nodes have empty embeddings",
			nodes:     []ClusterableNode{node("a", nil), node("b", nil)},
			threshold: 0.9,
		},
		{
			name:         "single node",
			nodes:        []ClusterableNode{node("a", []float32{1, 0})},
			threshold:    0.9,
			clusterSizes: []int{1},
		},
		{
			name: "two identical-direction nodes cluster together",
			nodes: []ClusterableNode{
				node("a", []float32{1, 0}),
				node("b", []float32{1, 0}),
			},
			threshold:    0.9,
			clusterSizes: []int{2},
		},
		{
			name: "two orthogonal nodes form separate clusters",
			nodes: []ClusterableNode{
				node("a", []float32{1, 0}),
				node("b", []float32{0, 1}),
			},
			threshold:    0.5,
			clusterSizes: []int{1, 1},
		},
		{
			name: "threshold zero puts all non-negative vectors in one cluster",
			nodes: []ClusterableNode{
				node("a", []float32{1, 0}),
				node("b", []float32{0, 1}),
				node("c", []float32{1, 1}),
			},
			threshold:    0.0,
			clusterSizes: []int{3},
		},
		{
			name: "threshold one clusters only identical vectors",
			nodes: []ClusterableNode{
				node("a", []float32{1, 0}),
				node("b", []float32{1, 0}),
				node("c", []float32{0, 1}),
			},
			threshold:    1.0,
			clusterSizes: []int{2, 1},
		},
		{
			name: "node with empty embedding is skipped",
			nodes: []ClusterableNode{
				node("a", []float32{1, 0}),
				node("b", nil),
				node("c", []float32{1, 0}),
			},
			threshold:    0.9,
			clusterSizes: []int{2},
		},
		{
			name: "greedy assignment: first node seeds cluster second evaluates against it",
			nodes: []ClusterableNode{
				node("a", []float32{1, 0}),
				node("b", []float32{0, 1}), // orthogonal → new cluster
				node("c", []float32{1, 0}), // matches cluster 0
			},
			threshold:    0.9,
			clusterSizes: []int{2, 1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClusterByRadius(tc.nodes, tc.threshold)

			if len(got) != len(tc.clusterSizes) {
				t.Fatalf("ClusterByRadius() returned %d clusters, want %d", len(got), len(tc.clusterSizes))
			}
			for i, want := range tc.clusterSizes {
				if len(got[i]) != want {
					t.Errorf("cluster[%d] has %d members, want %d", i, len(got[i]), want)
				}
			}
		})
	}
}
