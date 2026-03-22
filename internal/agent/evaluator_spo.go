package agent

import (
	"context"
	"time"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/utils"
)

const spoEnrichmentSystemPrompt = `You are a fact classifier. If the given fact describes a directed relationship between two specific named entities, output it as a single triple:

Subject | predicate | Object

Use snake_case for the predicate (e.g. works_at, is_married_to, is_child_of, prefers, owns).

If the fact is an observation, attribute, or insight that cannot be cleanly expressed as a Subject→predicate→Object relationship between two named entities, output nothing at all.

Output only the triple line or nothing.`

// runSPOEnrichment is a fire-and-forget goroutine that attempts to extract a
// Subject|predicate|Object triple from an already-stored evaluator fact and
// patch the node with that edge. It is intentionally best-effort: failures are
// logged at Debug level and do not affect the calling request.
func runSPOEnrichment(ctx context.Context, app *infra.App, fact, nodeType, domain string, significance float64, entryUUID string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cfg := app.Config()
	if cfg == nil {
		return
	}

	userPrompt := "Fact: " + utils.WrapAsUserData(utils.SanitizePrompt(fact))
	raw, err := infra.GenerateContentSimple(ctx, app, spoEnrichmentSystemPrompt+prompts.DataSafety(), userPrompt, cfg, &infra.GenConfig{MaxOutputTokens: 48})
	if err != nil || raw == "" {
		if err != nil {
			infra.LoggerFrom(ctx).Debug("spo enrichment LLM failed", "error", err, "fact", fact)
		}
		return
	}

	triple := memory.ParseSPOTriple(raw)
	if triple == nil {
		return
	}

	spo := &memory.SPOExtra{
		Predicate:   memory.NormalizedPredicate(triple.Predicate),
		ObjectValue: triple.Object,
	}

	writeCtx, writeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer writeCancel()

	if _, err := app.MemoryKnowledge().Upsert(writeCtx, fact, nodeType, domain, significance, memory.UpsertOptions{
		SPO:             spo,
		JournalEntryIDs: []string{entryUUID},
	}); err != nil {
		infra.LoggerFrom(ctx).Debug("spo enrichment upsert failed", "error", err, "predicate", spo.Predicate)
		return
	}
	infra.LoggerFrom(ctx).Info("spo enrichment stored", "predicate", spo.Predicate, "object", spo.ObjectValue, "fact", utils.TruncateString(fact, 60))
}
