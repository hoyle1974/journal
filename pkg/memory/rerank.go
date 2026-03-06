package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/llmjson"
	"github.com/jackstrohm/jot/pkg/infra"
)

const rerankSystemPrompt = "You are a re-ranker. Given a query and a numbered list of text items, return a JSON array of the item numbers (1-based indices) that best answer the query, ordered by relevance. Only include relevant items. Example: [3, 1, 5]."

// RerankNodes uses the LLM to re-rank knowledge nodes by relevance to the query.
func RerankNodes(ctx context.Context, query string, nodes []KnowledgeNode, topN int) ([]KnowledgeNode, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	if topN <= 0 {
		topN = len(nodes)
	}

	var sb strings.Builder
	for i, n := range nodes {
		content := n.Content
		if n.Metadata != "" && n.Metadata != "{}" {
			content = content + " " + n.Metadata
		}
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, content))
	}
	userPrompt := fmt.Sprintf("Query: %s\n\nNumbered items:\n%s\nReturn a JSON array of the 1-based indices that best answer the query, ordered by relevance (e.g. [2, 5, 1]).", query, sb.String())

	cfg := getConfigFromCtx(ctx)
	if cfg == nil {
		infra.LoggerFrom(ctx).Warn("rerank: no app config, using first topN")
		return firstN(nodes, topN), nil
	}
	jsonText, err := infra.GenerateContentSimple(ctx, rerankSystemPrompt, userPrompt, cfg, &infra.GenConfig{
		ResponseMIMEType: "application/json",
		MaxOutputTokens:  512,
	})
	if err != nil {
		infra.LoggerFrom(ctx).Warn("rerank generation failed, using first topN", "error", err)
		return firstN(nodes, topN), nil
	}

	var indices []int
	if err := llmjson.RepairAndUnmarshal(jsonText, &indices); err != nil {
		var floats []float64
		if err2 := llmjson.RepairAndUnmarshal(jsonText, &floats); err2 != nil {
			infra.LoggerFrom(ctx).Warn("rerank parse failed, using first topN", "error", err, "error2", err2)
			return firstN(nodes, topN), nil
		}
		indices = make([]int, 0, len(floats))
		for _, f := range floats {
			indices = append(indices, int(f))
		}
	}

	seen := make(map[int]bool)
	var result []KnowledgeNode
	for _, idx := range indices {
		if len(result) >= topN {
			break
		}
		if idx < 1 || idx > len(nodes) || seen[idx] {
			continue
		}
		seen[idx] = true
		result = append(result, nodes[idx-1])
	}
	if len(result) == 0 {
		return firstN(nodes, topN), nil
	}
	return result, nil
}

func firstN(nodes []KnowledgeNode, n int) []KnowledgeNode {
	if n <= 0 || len(nodes) == 0 {
		return nil
	}
	if n >= len(nodes) {
		return nodes
	}
	out := make([]KnowledgeNode, n)
	copy(out, nodes[:n])
	return out
}
