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
	SubType   string
	Predicate string
	Object    string
	ObjType   string
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
		Discovery:         utils.WrapAsUserData(discovery),
		Entry:             utils.WrapAsUserData(utils.SanitizePrompt(content)),
		AllowedPredicates: utils.WrapAsUserData(strings.Join(memory.AllowedPredicates, ", ")),
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

	for _, t := range triples {
		predicate, ok := memory.SnapAllowedPredicate(t.Predicate)
		if !ok {
			infra.LoggerFrom(ctx).Warn("refinery skipped triple with unmapped predicate", "entry_uuid", entryUUID, "predicate", t.Predicate, "subject", t.Subject, "object", t.Object)
			continue
		}
		subType := memory.CanonicalEntityNodeType(t.SubType)
		objType := memory.CanonicalEntityNodeType(t.ObjType)
		subj, err := app.Memory.EnsureNode(ctx, t.Subject, subType, entryUUID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("refinery ensure subject failed", "entry_uuid", entryUUID, "subject", t.Subject, "subject_type", subType, "error", err)
			continue
		}
		obj, err := app.Memory.EnsureNode(ctx, t.Object, objType, entryUUID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("refinery ensure object failed", "entry_uuid", entryUUID, "object", t.Object, "object_type", objType, "error", err)
			continue
		}
		relID, err := app.Memory.CreateRelationshipNode(ctx, subj.UUID, predicate, obj.UUID, entryUUID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("refinery create relationship failed", "entry_uuid", entryUUID, "subject_uuid", subj.UUID, "predicate", predicate, "object_uuid", obj.UUID, "error", err)
			continue
		}
		if err := app.Memory.AddEntityLink(ctx, subj.UUID, relID); err != nil {
			infra.LoggerFrom(ctx).Warn("refinery subject backlink failed", "entry_uuid", entryUUID, "node_uuid", subj.UUID, "relationship_uuid", relID, "error", err)
		}
		if err := app.Memory.AddEntityLink(ctx, obj.UUID, relID); err != nil {
			infra.LoggerFrom(ctx).Warn("refinery object backlink failed", "entry_uuid", entryUUID, "node_uuid", obj.UUID, "relationship_uuid", relID, "error", err)
		}
		if err := app.Memory.AddEntityLink(ctx, entryUUID, relID); err != nil {
			infra.LoggerFrom(ctx).Warn("refinery source-log backlink failed", "entry_uuid", entryUUID, "relationship_uuid", relID, "error", err)
		}
	}
	return nil
}

func parseRefineryTriples(lines []string) []refineryTriple {
	out := make([]refineryTriple, 0, len(lines))
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) != 5 && len(parts) != 3 {
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		sub := parts[0]
		pred := memory.NormalizedPredicate(parts[1])
		obj := parts[2]
		if sub == "" || pred == "" || obj == "" {
			continue
		}
		subType := memory.NodeTypePerson
		objType := memory.NodeTypePerson
		if len(parts) == 5 {
			subType = memory.CanonicalEntityNodeType(parts[3])
			objType = memory.CanonicalEntityNodeType(parts[4])
		}
		out = append(out, refineryTriple{
			Subject:   sub,
			SubType:   subType,
			Predicate: pred,
			Object:    obj,
			ObjType:   objType,
		})
	}
	return out
}
