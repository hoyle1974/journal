package memory

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

const rerankSystemPrompt = "You are a re-ranker. Given a query and a numbered list of text items, output the 1-based item numbers that best answer the query, ordered by relevance. Only include relevant items. Output structured key/value lines only. No JSON, no markdown.\n\nindices:\n<number>\n(one index per line, e.g. 3 then 1 then 5)"

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
	userPrompt := fmt.Sprintf("Query: %s\n\nNumbered items:\n%s\nOutput the 1-based indices that best answer the query, one per line under 'indices:'.", query, sb.String())

	cfg := getConfigFromCtx(ctx)
	if cfg == nil {
		infra.LoggerFrom(ctx).Warn("rerank: no app config, using first topN")
		return firstN(nodes, topN), nil
	}
	text, err := infra.GenerateContentSimple(ctx, rerankSystemPrompt, userPrompt, cfg, &infra.GenConfig{
		MaxOutputTokens: 512,
	})
	if err != nil {
		infra.LoggerFrom(ctx).Warn("rerank generation failed, using first topN", "error", err)
		return firstN(nodes, topN), nil
	}

	_, sections := utils.ParseKeyValueMap(text)
	lineStrs := sections["indices"]
	var indices []int
	for _, s := range lineStrs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			continue
		}
		indices = append(indices, n)
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
