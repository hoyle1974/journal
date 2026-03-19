package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
)

const tagConsolidatorMinTags = 5

// ConsolidateTags calls the LLM to normalize synonymous tags into canonical slugs.
// It returns a map of raw_tag -> canonical_slug. If the tag list has fewer than
// tagConsolidatorMinTags unique entries, no LLM call is made and an identity mapping
// is returned instead.
func ConsolidateTags(ctx context.Context, app *infra.App, tags []string) (map[string]string, error) {
	ctx, span := infra.StartSpan(ctx, "tag_consolidator.consolidate")
	defer span.End()

	// Deduplicate input.
	seen := make(map[string]struct{}, len(tags))
	unique := make([]string, 0, len(tags))
	for _, t := range tags {
		if _, ok := seen[t]; !ok && t != "" {
			seen[t] = struct{}{}
			unique = append(unique, t)
		}
	}

	// Build identity mapping helper.
	identity := func(ts []string) map[string]string {
		m := make(map[string]string, len(ts))
		for _, t := range ts {
			m[t] = t
		}
		return m
	}

	if len(unique) < tagConsolidatorMinTags {
		infra.LoggerFrom(ctx).Debug("tag_consolidator: skipping LLM (too few tags)", "count", len(unique))
		span.SetAttributes(map[string]string{"skipped": "true", "reason": "too_few_tags"})
		return identity(unique), nil
	}

	if app == nil || app.Config() == nil {
		return nil, fmt.Errorf("tag_consolidator: app config required")
	}

	tagList := strings.Join(unique, "\n")
	prompt, err := prompts.BuildTagConsolidator(prompts.TagConsolidatorData{TagList: tagList})
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("tag_consolidator build prompt: %w", err)
	}

	infra.LoggerFrom(ctx).Debug("tag_consolidator: calling LLM", "tag_count", len(unique), "tags", tagList)

	req := &infra.LLMRequest{
		Parts:     []*genai.Part{{Text: prompt}},
		Model:     app.Config().DreamerModel,
		GenConfig: &infra.GenConfig{MaxOutputTokens: 512},
	}
	apiCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	infra.GeminiCallsTotal.Inc()
	resp, err := app.Dispatch(apiCtx, req)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("tag_consolidator dispatch: %w", infra.WrapLLMError(err))
	}

	text := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	infra.LoggerFrom(ctx).Debug("tag_consolidator: raw LLM response", "response", text)

	// Parse "raw_tag=canonical_slug" lines. Use ParseKeyValueMap which handles "key: value" and "key=value"
	// by splitting on the first "=".
	mapping := make(map[string]string, len(unique))
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		raw := strings.TrimSpace(line[:idx])
		canonical := strings.TrimSpace(line[idx+1:])
		if raw == "" || canonical == "" {
			continue
		}
		mapping[raw] = canonical
	}

	// Ensure every unique input tag is represented; fill missing with identity.
	for _, t := range unique {
		if _, ok := mapping[t]; !ok {
			infra.LoggerFrom(ctx).Debug("tag_consolidator: missing tag in LLM output, using identity", "tag", t)
			mapping[t] = t
		}
	}

	span.SetAttributes(map[string]string{
		"input_tags":  fmt.Sprintf("%d", len(unique)),
		"output_tags": fmt.Sprintf("%d", len(mapping)),
	})
	infra.LoggerFrom(ctx).Info("tag_consolidator: consolidation complete", "input_tags", len(unique), "output_tags", len(mapping))
	return mapping, nil
}

