package agent

import (
	"context"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
)

// LinkEntryToPeople appends the given journal entry to each mentioned person node's journal_entry_ids
// so get_entity_network and other tools can surface that entry when viewing the entity.
func LinkEntryToPeople(ctx context.Context, app *infra.App, entryUUID string, entities []journal.Entity) {
	for _, ent := range entities {
		if ent.Type != "person" {
			continue
		}
		personNode, err := memory.FindEntityNodeByName(ctx, app, ent.Name)
		if err != nil || personNode == nil {
			continue
		}
		if err := memory.AppendJournalEntryIDsToNode(ctx, app, personNode.UUID, []string{entryUUID}); err != nil {
			infra.LoggerFrom(ctx).Debug("graph link append failed", "person", ent.Name, "entry", entryUUID, "error", err)
			continue
		}
		infra.LoggerFrom(ctx).Debug("graph link created", "person", ent.Name, "entry", entryUUID, "reason", "automatic backlink during processing")
	}
}
