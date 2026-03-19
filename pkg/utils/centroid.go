package utils

// ClusterableNode is a node that can be clustered by embedding similarity.
type ClusterableNode struct {
	ID        string
	Embedding []float32
	Text      string
}

// MeanVector returns the element-wise mean of the given vectors.
// Returns nil if vecs is empty or contains vectors of inconsistent length.
func MeanVector(vecs [][]float32) []float32 {
	if len(vecs) == 0 {
		return nil
	}
	dim := len(vecs[0])
	if dim == 0 {
		return nil
	}
	sum := make([]float32, dim)
	for _, v := range vecs {
		if len(v) != dim {
			return nil
		}
		for i, x := range v {
			sum[i] += x
		}
	}
	n := float32(len(vecs))
	for i := range sum {
		sum[i] /= n
	}
	return sum
}

// ClusterByRadius groups nodes into clusters using greedy radius clustering.
// The first unassigned node seeds a new cluster. Each subsequent node joins the cluster
// whose current centroid has CosineSimilarity >= threshold; if no cluster matches, a new
// cluster is started. The centroid is recalculated after each addition.
// Returns a list of clusters; each cluster is a slice of ClusterableNode.
// Nodes with empty embeddings are skipped.
func ClusterByRadius(nodes []ClusterableNode, threshold float64) [][]ClusterableNode {
	type clusterState struct {
		members  []ClusterableNode
		centroid []float32
	}

	var clusters []*clusterState

	for _, node := range nodes {
		if len(node.Embedding) == 0 {
			continue
		}

		// Try to find a matching cluster.
		bestIdx := -1
		bestSim := -1.0
		for i, c := range clusters {
			sim := CosineSimilarity(node.Embedding, c.centroid)
			if sim >= threshold && sim > bestSim {
				bestSim = sim
				bestIdx = i
			}
		}

		if bestIdx >= 0 {
			// Join the best matching cluster and update centroid.
			c := clusters[bestIdx]
			c.members = append(c.members, node)
			vecs := make([][]float32, 0, len(c.members))
			for _, m := range c.members {
				vecs = append(vecs, m.Embedding)
			}
			c.centroid = MeanVector(vecs)
		} else {
			// Seed a new cluster.
			centroid := make([]float32, len(node.Embedding))
			copy(centroid, node.Embedding)
			clusters = append(clusters, &clusterState{
				members:  []ClusterableNode{node},
				centroid: centroid,
			})
		}
	}

	result := make([][]ClusterableNode, 0, len(clusters))
	for _, c := range clusters {
		result = append(result, c.members)
	}
	return result
}
