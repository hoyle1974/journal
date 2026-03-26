package agent

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/utils"
)

const (
	dreamSelfCheckBoost     = 0.05
	dreamSelfCheckMaxWeight = 1.0
)

// dreamSelfCheckQuestion asks the LLM whether the knowledge graph can already
// answer a candidate question. If yes, it boosts the significance_weight of the
// answer nodes and returns true (caller should drop the question). On any error
// it returns false so the question is kept rather than silently lost.
func dreamSelfCheckQuestion(ctx context.Context, app *infra.App, question string) (bool, error) {
	log := infra.LoggerFrom(ctx)

	ragCtx, err := BuildLoomRAGContext(ctx, app, "", question, nil)
	if err != nil || ragCtx == nil {
		log.Warn("dreamer self-check: RAG context unavailable (keeping question)", "error", err)
		return false, nil
	}
	graphText := ragCtx.FormatForPrompt()
	if graphText == "" {
		return false, nil // no context → can't self-answer
	}

	prompt, err := prompts.BuildDreamerSelfCheck(prompts.DreamerSelfCheckData{
		Question:     utils.WrapAsUserData(question),
		GraphContext: utils.WrapAsUserData(graphText),
	})
	if err != nil {
		return false, fmt.Errorf("dreamer self-check: build prompt: %w", err)
	}

	raw, err := infra.GenerateContentSimple(ctx, app, "", prompt+prompts.DataSafety(), app.Config(), &infra.GenConfig{MaxOutputTokens: 200})
	if err != nil {
		log.Warn("dreamer self-check: LLM call failed (keeping question)", "error", err)
		return false, nil
	}

	log.Debug("dreamer self-check: LLM response", "question", question, "response", raw)
	kv, _ := utils.ParseKeyValueMap(raw)
	if !strings.EqualFold(strings.TrimSpace(kv["can_answer"]), "yes") {
		return false, nil
	}

	// Boost significance of the nodes that answered the question.
	for _, id := range strings.Split(kv["node_ids"], ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if boostErr := boostNodeSignificance(ctx, app, id); boostErr != nil {
			log.Warn("dreamer self-check: failed to boost node", "node_id", id, "error", boostErr)
		} else {
			log.Info("dreamer self-check: boosted node significance", "node_id", id)
		}
	}

	log.Info("dreamer self-check: question answerable from graph, dropping", "question", question)
	return true, nil
}

// boostNodeSignificance increments a node's significance_weight by dreamSelfCheckBoost,
// capped at dreamSelfCheckMaxWeight.
func boostNodeSignificance(ctx context.Context, app *infra.App, nodeID string) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("boost node: firestore: %w", err)
	}
	ref := client.Collection(memory.KnowledgeCollection).Doc(nodeID)
	return client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if err != nil {
			return fmt.Errorf("boost node: get: %w", err)
		}
		current, _ := doc.Data()["significance_weight"].(float64)
		newWeight := current + dreamSelfCheckBoost
		if newWeight > dreamSelfCheckMaxWeight {
			newWeight = dreamSelfCheckMaxWeight
		}
		return tx.Update(ref, []firestore.Update{
			{Path: "significance_weight", Value: newWeight},
		})
	})
}
