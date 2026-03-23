package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/utils"
)

type refineryTriple struct {
	Subject   string
	Predicate string
	Object    string
}

func runRefineryPipeline(ctx context.Context, app *infra.App, entryUUID, content string) error {
	ctx, span := infra.StartSpan(ctx, "agent.refinery_pipeline")
	defer span.End()
	span.SetAttributes(map[string]string{"entry_uuid": entryUUID})

	discovery, err := refineryDiscovery(ctx, app, content)
	if err != nil {
		return fmt.Errorf("refinery discovery: %w", err)
	}
	triples, err := refineryExtract(ctx, app, entryUUID, content, discovery)
	if err != nil {
		return fmt.Errorf("refinery extract: %w", err)
	}
	if len(triples) == 0 {
		infra.LoggerFrom(ctx).Debug("refinery: no triples", "entry_uuid", entryUUID)
		return nil
	}
	return refineryResolveCommit(ctx, app, entryUUID, triples)
}

func refineryDiscovery(ctx context.Context, app *infra.App, content string) (string, error) {
	ctx, span := infra.StartSpan(ctx, "agent.refinery_discovery")
	defer span.End()

	queryVec, err := infra.GenerateEmbedding(ctx, app.Config().GoogleCloudProject, content, infra.EmbedTaskRetrievalQuery)
	if err != nil {
		return "", fmt.Errorf("discovery embedding: %w", err)
	}
	nodes, err := app.Memory.QuerySimilarNodes(ctx, queryVec, 12)
	if err != nil {
		return "", fmt.Errorf("discovery query similar: %w", err)
	}
	var b strings.Builder
	for _, n := range nodes {
		fmt.Fprintf(&b, "- %s | %s | %s\n", n.UUID, n.NodeType, n.Content)
	}
	return strings.TrimSpace(b.String()), nil
}

func refineryExtract(ctx context.Context, app *infra.App, entryUUID, content, discovery string) ([]refineryTriple, error) {
	ctx, span := infra.StartSpan(ctx, "agent.refinery_extract")
	defer span.End()

	prompt, err := prompts.BuildRefinery(prompts.RefineryData{
		Discovery: utils.WrapAsUserData(discovery),
		Entry:     utils.WrapAsUserData(utils.SanitizePrompt(content)),
	})
	if err != nil {
		return nil, fmt.Errorf("build refinery prompt: %w", err)
	}
	raw, err := infra.GenerateContentSimple(ctx, app, prompt+prompts.DataSafety(), "", app.Config(), &infra.GenConfig{MaxOutputTokens: 300})
	if err != nil {
		return nil, fmt.Errorf("refinery llm call: %w", err)
	}
	infra.LoggerFrom(ctx).Debug("refinery raw output", "entry_uuid", entryUUID, "output", raw)
	simple, sections := utils.ParseKeyValueMap(raw)
	if strings.EqualFold(simple["status"], "none") {
		return nil, nil
	}
	lines := sections["triples"]
	return parseRefineryTriples(lines), nil
}

func refineryResolveCommit(ctx context.Context, app *infra.App, entryUUID string, triples []refineryTriple) error {
	ctx, span := infra.StartSpan(ctx, "agent.refinery_resolve_commit")
	defer span.End()
	span.SetAttributes(map[string]string{"entry_uuid": entryUUID})

	allowed := map[string]struct{}{
		"works_at":          {},
		"prefers":           {},
		"owns":              {},
		"lives_in":          {},
		"located_in":        {},
		"collaborates_with": {},
		"reports_to":        {},
		"manages":           {},
	}

	for _, t := range triples {
		if _, ok := allowed[t.Predicate]; !ok {
			infra.LoggerFrom(ctx).Warn("refinery skipped triple with non-ontology predicate", "entry_uuid", entryUUID, "predicate", t.Predicate, "subject", t.Subject, "object", t.Object)
			continue
		}
		subj, err := app.Memory.EnsureNode(ctx, t.Subject, memory.NodeTypePerson, entryUUID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("refinery ensure subject failed", "entry_uuid", entryUUID, "subject", t.Subject, "error", err)
			continue
		}
		obj, err := app.Memory.EnsureNode(ctx, t.Object, memory.NodeTypePerson, entryUUID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("refinery ensure object failed", "entry_uuid", entryUUID, "object", t.Object, "error", err)
			continue
		}
		relID, err := app.Memory.CreateRelationshipNode(ctx, subj.UUID, t.Predicate, obj.UUID, entryUUID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("refinery create relationship failed", "entry_uuid", entryUUID, "subject_uuid", subj.UUID, "predicate", t.Predicate, "object_uuid", obj.UUID, "error", err)
			continue
		}
		if err := app.Memory.AddEntityLink(ctx, subj.UUID, relID); err != nil {
			infra.LoggerFrom(ctx).Warn("refinery subject backlink failed", "entry_uuid", entryUUID, "node_uuid", subj.UUID, "relationship_uuid", relID, "error", err)
		}
		if err := app.Memory.AddEntityLink(ctx, obj.UUID, relID); err != nil {
			infra.LoggerFrom(ctx).Warn("refinery object backlink failed", "entry_uuid", entryUUID, "node_uuid", obj.UUID, "relationship_uuid", relID, "error", err)
		}
	}
	return nil
}

func parseRefineryTriples(lines []string) []refineryTriple {
	out := make([]refineryTriple, 0, len(lines))
	for _, line := range lines {
		spo := memory.ParseSPOTriple(line)
		if spo == nil {
			continue
		}
		out = append(out, refineryTriple{
			Subject:   strings.TrimSpace(spo.Subject),
			Predicate: memory.NormalizedPredicate(spo.Predicate),
			Object:    strings.TrimSpace(spo.Object),
		})
	}
	return out
}
