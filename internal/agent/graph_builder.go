package agent

import (
	"context"
	"strings"
	"time"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
)

// resolveTimeout caps the time spent on synchronous entity resolution per entry.
// Entity resolution performs vector searches per entity — budget 8 seconds total.
// Note: with multiple entities and ~500ms per vector search, ingest P99 latency may increase
// by 2-4 seconds. If observed in production, move ResolveAndLinkEntities to a goroutine.
const resolveTimeout = 8 * time.Second

// ResolveAndLinkEntities resolves all entity mentions (persons, places, orgs, etc.) from journal
// analysis against existing knowledge nodes and appends the entry UUID to each matched node's
// journal_entry_ids. Runs synchronously within resolveTimeout. Failures are logged and swallowed —
// this is a best-effort enrichment step. If no node exists for an entity, it is skipped (node
// creation happens via the Dreamer or explicit upsert_knowledge tool calls).
func ResolveAndLinkEntities(ctx context.Context, app *infra.App, entryUUID string, entities []memory.Entity) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	ctx, span := infra.StartSpan(ctx, "agent.resolve_and_link_entities")
	defer span.End()

	for _, ent := range entities {
		if strings.TrimSpace(ent.Name) == "" {
			continue
		}
		node, err := app.Memory.FindEntityNodeByName(ctx, ent.Name)
		if err != nil {
			infra.LoggerFrom(ctx).Debug("resolve_entities find error", "entity", ent.Name, "error", err)
			continue
		}
		if node == nil {
			infra.LoggerFrom(ctx).Debug("resolve_entities no match", "entity", ent.Name, "type", ent.Type)
			continue
		}
		if err := app.Memory.AppendJournalEntryIDsToNode(ctx, node.UUID, []string{entryUUID}); err != nil {
			infra.LoggerFrom(ctx).Debug("resolve_entities link failed", "entity", ent.Name, "node", node.UUID, "error", err)
			continue
		}
		infra.LoggerFrom(ctx).Debug("resolve_entities linked", "entity", ent.Name, "node_type", node.NodeType, "node_uuid", node.UUID, "entry", entryUUID)
	}
}
