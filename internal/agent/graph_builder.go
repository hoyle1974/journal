package agent

import (
	"context"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

// resolveTimeout caps the time spent on synchronous entity resolution per entry.
// Entity resolution performs vector searches per entity — budget 8 seconds total.
// Note: with multiple entities and ~500ms per vector search, ingest P99 latency may increase
// by 2-4 seconds. If observed in production, move ResolveAndLinkEntities to a goroutine.
const resolveTimeout = 8 * time.Second

// parseSPOLines parses LLM output lines into SPO triples.
// Skips "NONE", blank lines, and lines without exactly two "|" separators.
// Normalizes predicates to snake_case via memory.NormalizedPredicate.
func parseSPOLines(output string) []memory.SPOTriple {
	var result []memory.SPOTriple
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "NONE") {
			continue
		}
		triple := memory.ParseSPOTriple(line)
		if triple == nil {
			continue
		}
		triple.Predicate = memory.NormalizedPredicate(triple.Predicate)
		result = append(result, *triple)
	}
	return result
}

// ResolveAndLinkEntities resolves all entity mentions (persons, places, orgs, etc.) from journal
// analysis against existing knowledge nodes and appends the entry UUID to each matched node's
// journal_entry_ids. Runs synchronously within resolveTimeout. Failures are logged and swallowed —
// this is a best-effort enrichment step. If no node exists for an entity, it is skipped (node
// creation happens via the Dreamer or explicit upsert_knowledge tool calls).
func ResolveAndLinkEntities(ctx context.Context, app *infra.App, entryUUID string, entities []journal.Entity) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	ctx, span := infra.StartSpan(ctx, "agent.resolve_and_link_entities")
	defer span.End()

	for _, ent := range entities {
		if strings.TrimSpace(ent.Name) == "" {
			continue
		}
		node, err := memory.FindEntityNodeByName(ctx, app, ent.Name)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("resolve_entities find error", "entity", ent.Name, "error", err)
			continue
		}
		if node == nil {
			infra.LoggerFrom(ctx).Debug("resolve_entities no match", "entity", ent.Name, "type", ent.Type)
			continue
		}
		if err := memory.AppendJournalEntryIDsToNode(ctx, app, node.UUID, []string{entryUUID}); err != nil {
			infra.LoggerFrom(ctx).Debug("resolve_entities link failed", "entity", ent.Name, "node", node.UUID, "error", err)
			continue
		}
		infra.LoggerFrom(ctx).Debug("resolve_entities linked", "entity", ent.Name, "node_type", node.NodeType, "node_uuid", node.UUID, "entry", entryUUID)
	}
}

// ExtractAndStoreRelationships calls the LLM to extract SPO triples from the entry content,
// then upserts each triple as a generic knowledge node and links the subject and object nodes.
// Must be called in a goroutine — it makes an LLM call (~1-2s) that would unacceptably extend
// synchronous ingest latency. Failures are logged and swallowed; this must not block ingest.
func ExtractAndStoreRelationships(ctx context.Context, app *infra.App, entryUUID, content string) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	ctx, span := infra.StartSpan(ctx, "agent.extract_and_store_relationships")
	defer span.End()

	if len(strings.TrimSpace(content)) < 20 {
		return
	}

	prompt, err := prompts.BuildRelationshipExtractor(prompts.RelationshipExtractorData{Content: content})
	if err != nil {
		infra.LoggerFrom(ctx).Debug("relationship_extractor render failed", "error", err)
		return
	}

	// prompt contains both system instructions and the wrapped entry content.
	// Pass as systemPrompt; userPrompt is empty (matching the evaluator pattern in specialists.go).
	raw, err := infra.GenerateContentSimple(ctx, app, prompt, "", app.Config(), &infra.GenConfig{MaxOutputTokens: 256})
	if err != nil {
		infra.LoggerFrom(ctx).Debug("relationship_extractor llm failed", "error", err)
		return
	}
	infra.LoggerFrom(ctx).Debug("relationship_extractor raw output", "output", raw)

	triples := parseSPOLines(raw)
	if len(triples) == 0 {
		return
	}

	for _, triple := range triples {
		// e.g. "Gloria works_at Anthropic"
		nodeContent := triple.Subject + " " + triple.Predicate + " " + triple.Object

		subjNode, _ := memory.FindEntityNodeByName(ctx, app, triple.Subject)
		objNode, _ := memory.FindEntityNodeByName(ctx, app, triple.Object)

		entityLinks := []string{entryUUID}
		if subjNode != nil {
			entityLinks = append(entityLinks, subjNode.UUID)
		}
		if objNode != nil {
			entityLinks = append(entityLinks, objNode.UUID)
		}

		metaMap := map[string]any{
			// TruncateString is used here for Firestore metadata storage, not logging — the
			// no-truncation rule applies to Debug log values only.
			"source_excerpt": utils.TruncateString(content, 200),
			"extracted_facts": []string{
				triple.Subject + " | " + triple.Predicate + " | " + triple.Object,
			},
			"confidence_score": 0.8,
		}
		metaJSON, err := memory.MetadataToJSON(metaMap)
		if err != nil {
			metaJSON = "{}"
		}

		spoUUID, err := memory.UpsertKnowledge(ctx, app, nodeContent, memory.NodeTypeGeneric, metaJSON, entityLinks)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("relationship upsert failed", "triple", nodeContent, "error", err)
			continue
		}
		infra.LoggerFrom(ctx).Debug("relationship stored", "spo_uuid", spoUUID, "content", nodeContent)

		// Link subject node back to this SPO node for bidirectional traversal.
		if subjNode != nil {
			if err := memory.AddEntityLink(ctx, app, subjNode.UUID, spoUUID); err != nil {
				infra.LoggerFrom(ctx).Debug("spo subject backlink failed", "error", err)
			}
		}
	}

	infra.LoggerFrom(ctx).Debug("relationship_extractor done", "entry_uuid", entryUUID, "triples_stored", len(triples))
}
