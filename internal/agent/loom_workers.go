package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/memory"
	"github.com/jackstrohm/jot/internal/infra"
)

// runTaskWorker scans logContent for commitment signals and, when the confidence threshold
// is met, queues a PendingQuestion of kind KindTaskProposal so the user is prompted to
// confirm task creation on their next Telegram interaction.
// extractedObjectIDs are the UUIDs of object/log nodes associated with this pipeline run.
func runTaskWorker(ctx context.Context, app *infra.App, logContent string, extractedObjectIDs []string) error {
	parsed, err := RunEvaluatorExtract(ctx, app, logContent)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("loom task worker: evaluator extract failed", "error", err)
		return err
	}
	if parsed == nil {
		return nil
	}
	if parsed.FutureCommitment < AgencyTaskCommitmentThreshold {
		return nil
	}
	intent := strings.TrimSpace(parsed.CommitmentIntent)
	if len(intent) < MinCommitmentIntentLen {
		return nil
	}
	q := memory.PendingQuestion{
		Question:       fmt.Sprintf("Should I create a task for: \"%s\"?", intent),
		Kind:           memory.KindTaskProposal,
		Context:        intent,
		SourceEntryIDs: extractedObjectIDs,
	}
	if err := app.Memory.InsertPendingQuestions(ctx, []memory.PendingQuestion{q}); err != nil {
		infra.LoggerFrom(ctx).Warn("loom task worker: insert task proposal failed", "error", err)
		return err
	}
	infra.LoggerFrom(ctx).Info("loom task worker: task proposal queued", "intent", intent)
	return nil
}

