package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/api/iterator"
)

const (
	// CentroidClusterThreshold is the cosine similarity threshold for radius clustering.
	CentroidClusterThreshold = 0.88
	// CentroidMinClusterSize is the minimum number of nodes to promote a cluster to a Context.
	CentroidMinClusterSize = 5
	// CentroidLookbackDays is how many days back to query for recent knowledge nodes.
	CentroidLookbackDays = 7
	// CentroidSignificanceMin is the minimum significance_weight for nodes to be considered.
	CentroidSignificanceMin = 0.7
)

// centroidNode is an internal struct for nodes fetched for centroid clustering.
type centroidNode struct {
	uuid        string
	embedding   []float32
	content     string
	entityLinks []string
}

// fetchRecentHighValueNodes queries Firestore for knowledge nodes from the last
// CentroidLookbackDays with significance_weight >= CentroidSignificanceMin.
// Excludes log entries and context nodes.
func fetchRecentHighValueNodes(ctx context.Context, app *infra.App) ([]centroidNode, error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("centroid incubator: get firestore client: %w", err)
	}

	cutoff := time.Now().AddDate(0, 0, -CentroidLookbackDays).Format(time.RFC3339)
	iter := client.Collection(memory.KnowledgeCollection).
		Where("significance_weight", ">=", CentroidSignificanceMin).
		Where("timestamp", ">=", cutoff).
		Documents(ctx)
	defer iter.Stop()

	var nodes []centroidNode
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("centroid incubator: iterate nodes: %w", infra.WrapFirestoreIndexError(err))
		}

		data := doc.Data()
		nodeType := infra.GetStringField(data, "node_type")
		// Skip log entries and context nodes — we cluster fact/person/project/etc. nodes only.
		if nodeType == "log" || nodeType == memory.ContextNodeType {
			continue
		}

		// Read the embedding. Firestore stores Vector32 as a native vector type;
		// when read back via doc.Data(), it may be []float32 directly or as firestore.Vector32.
		var emb []float32
		if v, ok := data["embedding"].(firestore.Vector32); ok && len(v) > 0 {
			emb = []float32(v)
		} else {
			// Fallback: try []float32 directly (some SDK versions)
			if v2, ok2 := data["embedding"].([]float32); ok2 && len(v2) > 0 {
				emb = v2
			}
		}
		if len(emb) == 0 {
			infra.LoggerFrom(ctx).Debug("centroid incubator: skip node missing embedding", "uuid", doc.Ref.ID)
			continue
		}

		entityLinks := infra.GetStringSliceField(data, "entity_links")
		content := infra.GetStringField(data, "content")

		nodes = append(nodes, centroidNode{
			uuid:        doc.Ref.ID,
			embedding:   emb,
			content:     content,
			entityLinks: entityLinks,
		})
	}
	return nodes, nil
}

// nameClusterWithLLM calls the LLM to produce a short slug name for a cluster of nodes.
// Returns a normalized slug (lowercased, underscored).
func nameClusterWithLLM(ctx context.Context, app *infra.App, texts []string) (string, error) {
	systemPrompt := `You are a semantic theme labeler. Given a list of related knowledge facts, output a single short slug name (2-4 words, lowercase, underscores) that captures the common theme or project. Output ONLY one key/value line: name=<slug>. No explanations.`

	var sb strings.Builder
	sb.WriteString("Facts in this cluster:\n")
	for i, t := range texts {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, t))
	}
	userPrompt := utils.WrapAsUserData(utils.SanitizePrompt(sb.String()))

	raw, err := infra.GenerateContentSimple(ctx, app, systemPrompt, userPrompt, app.Config(), &infra.GenConfig{
		MaxOutputTokens: 64,
		ModelOverride:   app.DreamerModel(),
	})
	if err != nil {
		return "", fmt.Errorf("centroid incubator: LLM name cluster: %w", err)
	}

	infra.LoggerFrom(ctx).Debug("centroid incubator: LLM cluster name raw output", "raw", raw)

	simple, _ := utils.ParseKeyValueMap(raw)
	name := strings.TrimSpace(simple["name"])
	if name == "" {
		// Fallback: use first 40 chars of the first text, normalized.
		name = "cluster"
	}
	// Normalize: lowercase, replace spaces/hyphens with underscores.
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	if len(name) > 60 {
		name = name[:60]
	}
	return name, nil
}

// clusterHasActiveContextLink returns true if any node in the cluster already
// has an entity_link to a known context node UUID. We detect this by checking
// whether FindContextByName returns a context whose UUID is in any node's entity_links.
func clusterHasActiveContextLink(ctx context.Context, app *infra.App, nodes []centroidNode, contextName string) (bool, string) {
	existing, _, err := app.Memory.FindContextByName(ctx, contextName)
	if err != nil || existing == nil {
		return false, ""
	}
	// Check if any node already links to this context.
	ctxUUID := existing.UUID
	for _, n := range nodes {
		for _, link := range n.entityLinks {
			if link == ctxUUID {
				return true, ctxUUID
			}
		}
	}
	// Context exists but no nodes link to it yet — still return its UUID so we can link them.
	return false, ctxUUID
}

// RunCentroidIncubation surfaces invisible themes by clustering recent high-value knowledge
// nodes by embedding similarity, then auto-labels and promotes them as Contexts.
// It is designed to be called from the Dreamer after the specialist extraction phase
// but before the synthesis phase.
func RunCentroidIncubation(ctx context.Context, app *infra.App) error {
	ctx, span := infra.StartSpan(ctx, "centroid_incubator.run")
	defer span.End()

	if app == nil {
		return fmt.Errorf("centroid incubator: app required")
	}

	infra.LoggerFrom(ctx).Info("centroid incubation starting", "lookback_days", CentroidLookbackDays, "min_significance", CentroidSignificanceMin, "threshold", CentroidClusterThreshold, "min_cluster_size", CentroidMinClusterSize)

	// Step 1: Fetch recent high-value nodes.
	nodes, err := fetchRecentHighValueNodes(ctx, app)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("centroid incubator: fetch nodes: %w", err)
	}
	infra.LoggerFrom(ctx).Info("centroid incubation: fetched nodes", "count", len(nodes))
	if len(nodes) == 0 {
		infra.LoggerFrom(ctx).Debug("centroid incubation: no eligible nodes, skipping")
		return nil
	}

	// Step 2: Convert to ClusterableNode and run radius clustering.
	clusterInput := make([]utils.ClusterableNode, 0, len(nodes))
	nodeByID := make(map[string]centroidNode, len(nodes))
	for _, n := range nodes {
		clusterInput = append(clusterInput, utils.ClusterableNode{
			ID:        n.uuid,
			Embedding: n.embedding,
			Text:      n.content,
		})
		nodeByID[n.uuid] = n
	}

	clusters := utils.ClusterByRadius(clusterInput, CentroidClusterThreshold)
	infra.LoggerFrom(ctx).Info("centroid incubation: clustering complete", "total_nodes", len(nodes), "clusters_found", len(clusters))

	// Step 3: For each sufficiently large cluster, promote to a Context.
	promoted := 0
	for clusterIdx, cluster := range clusters {
		if len(cluster) < CentroidMinClusterSize {
			infra.LoggerFrom(ctx).Debug("centroid incubation: cluster too small, skipping", "cluster_idx", clusterIdx, "size", len(cluster))
			continue
		}

		infra.LoggerFrom(ctx).Info("centroid incubation: processing cluster", "cluster_idx", clusterIdx, "size", len(cluster))

		// Collect texts for LLM naming.
		texts := make([]string, 0, len(cluster))
		for _, cn := range cluster {
			if cn.Text != "" {
				texts = append(texts, cn.Text)
			}
		}
		infra.LoggerFrom(ctx).Debug("centroid incubation: cluster texts", "cluster_idx", clusterIdx, "texts", strings.Join(texts, " | "))

		// Step 3a: Name the cluster.
		contextName, nameErr := nameClusterWithLLM(ctx, app, texts)
		if nameErr != nil {
			infra.LoggerFrom(ctx).Warn("centroid incubation: failed to name cluster, skipping", "cluster_idx", clusterIdx, "error", nameErr)
			continue
		}
		infra.LoggerFrom(ctx).Info("centroid incubation: cluster named", "cluster_idx", clusterIdx, "name", contextName)

		// Step 3b: Find or create the Context. Check if nodes are already linked.
		alreadyLinked, existingCtxUUID := clusterHasActiveContextLink(ctx, app, func() []centroidNode {
			out := make([]centroidNode, 0, len(cluster))
			for _, cn := range cluster {
				if n, ok := nodeByID[cn.ID]; ok {
					out = append(out, n)
				}
			}
			return out
		}(), contextName)

		var contextUUID string
		if alreadyLinked {
			infra.LoggerFrom(ctx).Info("centroid incubation: cluster already linked to active context, skipping link step", "cluster_idx", clusterIdx, "context_name", contextName, "context_uuid", existingCtxUUID)
			continue
		}

		if existingCtxUUID != "" {
			// Context exists but nodes not yet linked — use existing context.
			contextUUID = existingCtxUUID
		} else {
			// Create (or find) the context.
			var ensureErr error
			contextUUID, ensureErr = app.Memory.EnsureContextExists(ctx, contextName)
			if ensureErr != nil {
				infra.LoggerFrom(ctx).Warn("centroid incubation: failed to ensure context exists", "cluster_idx", clusterIdx, "context_name", contextName, "error", ensureErr)
				continue
			}
		}

		infra.LoggerFrom(ctx).Info("centroid incubation: linking cluster nodes to context", "cluster_idx", clusterIdx, "context_name", contextName, "context_uuid", contextUUID, "node_count", len(cluster))

		// Step 3c: Link each node to the context.
		linksAdded := 0
		for _, cn := range cluster {
			if linkErr := app.Memory.AddEntityLink(ctx, cn.ID, contextUUID); linkErr != nil {
				infra.LoggerFrom(ctx).Warn("centroid incubation: failed to link node to context", "node_uuid", cn.ID, "context_uuid", contextUUID, "error", linkErr)
				continue
			}
			linksAdded++
		}

		infra.LoggerFrom(ctx).Info("centroid incubation: cluster promoted", "cluster_idx", clusterIdx, "context_name", contextName, "context_uuid", contextUUID, "links_added", linksAdded)
		promoted++
	}

	infra.LoggerFrom(ctx).Info("centroid incubation complete", "clusters_evaluated", len(clusters), "contexts_promoted", promoted)
	span.SetAttributes(map[string]string{
		"nodes_fetched":     fmt.Sprintf("%d", len(nodes)),
		"clusters_found":    fmt.Sprintf("%d", len(clusters)),
		"contexts_promoted": fmt.Sprintf("%d", promoted),
	})
	return nil
}
