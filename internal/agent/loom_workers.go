package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/utils"
)

// runTaskWorker scans logContent for commitment signals and auto-creates Task nodes
// after graph objects exist so the task can link to them.
// extractedObjectIDs are the UUIDs of object/log nodes associated with this pipeline run.
//
// This replaces the commitment-detection logic previously inside ProcessEntry.
func runTaskWorker(ctx context.Context, app *infra.App, logContent string, extractedObjectIDs []string) error {
	ctx, span := infra.StartSpan(ctx, "loom.task_worker")
	defer span.End()

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
	t := &memory.Task{
		Content:         intent,
		Status:          memory.TaskStatusPending,
		JournalEntryIDs: extractedObjectIDs,
	}
	taskUUID, err := app.Memory.CreateTask(ctx, t)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("loom task worker: create task failed", "error", err)
		return err
	}
	infra.LoggerFrom(ctx).Info("loom task worker: task created", "task_uuid", taskUUID, "intent", intent)
	return nil
}

// runResponseWorker builds 2-hop RAG context, calls the LLM for a proactive insight,
// and stores the result as a NodeTypeResponse node linked to the source log entry.
// graphExtractFailed signals that Stage 2 (refinery) did not complete — context is degraded.
func runResponseWorker(ctx context.Context, app *infra.App, logUUID, logContent string, graphExtractFailed bool) error {
	ctx, span := infra.StartSpan(ctx, "loom.response_worker")
	defer span.End()

	ragCtx, err := BuildLoomRAGContext(ctx, app, logContent)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("loom response worker: RAG context failed", "log_uuid", logUUID, "error", err)
		// Continue — ragCtx may be partially populated or empty, both are acceptable.
	}

	contextBlock := ""
	if ragCtx != nil {
		contextBlock = ragCtx.FormatForPrompt()
	}
	graphNote := ""
	if graphExtractFailed {
		graphNote = "\nNote: Knowledge graph extraction failed for this entry. Context may be incomplete.\n"
	}

	systemPrompt := "You are a personal memory assistant. Given the user's log entry and the related graph context, generate a concise, insightful response or observation. " +
		"Output your response on a line starting with 'response:' followed by your text. " +
		"Then output a 'logic_trace:' line with a brief paragraph explaining your reasoning." +
		prompts.DataSafety()

	userPrompt := fmt.Sprintf("%s\n## Log Entry\n%s\n\n## Graph Context\n%s",
		graphNote,
		utils.WrapAsUserData(utils.SanitizePrompt(logContent)),
		utils.WrapAsUserData(contextBlock),
	)

	raw, err := infra.GenerateContentSimple(ctx, app, systemPrompt, userPrompt, app.Config(), &infra.GenConfig{MaxOutputTokens: 512})
	if err != nil {
		return fmt.Errorf("loom response worker: LLM call: %w", err)
	}

	simple, _ := utils.ParseKeyValueMap(raw)
	responseText := strings.TrimSpace(simple["response"])
	logicTrace := strings.TrimSpace(simple["logic_trace"])
	if responseText == "" {
		// Fallback: use full LLM output if KV parsing found nothing.
		responseText = strings.TrimSpace(raw)
	}

	// Store as a NodeTypeResponse node linked to the source log entry.
	fsClient, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("loom response worker: firestore: %w", err)
	}
	respUUID := loomResponseUUID()
	respDoc := map[string]any{
		"content":         responseText,
		"node_type":       memory.NodeTypeResponse,
		"source_entry_id": logUUID,
		"logic_trace":     logicTrace,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	}
	if _, setErr := fsClient.Collection(memory.KnowledgeCollection).Doc(respUUID).Set(ctx, respDoc); setErr != nil {
		return fmt.Errorf("loom response worker: write response node: %w", setErr)
	}
	infra.LoggerFrom(ctx).Info("loom response worker: response node stored",
		"log_uuid", logUUID,
		"response_uuid", respUUID,
		"logic_trace_len", len(logicTrace),
	)
	return nil
}

// loomResponseUUID generates a prefixed UUID for response nodes.
func loomResponseUUID() string {
	return fmt.Sprintf("resp-%s", uuid.New().String())
}
