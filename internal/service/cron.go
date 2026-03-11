package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/api/iterator"
)

const (
	JanitorWeightThreshold   = 0.2
	JanitorStaleDays         = 30
	PulseStaleDays           = 14
	PulseImportanceThreshold = 0.7
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// PulseResult holds the outcome of a pulse audit run.
type PulseResult struct {
	StaleNodes []string
	Signals    int
}

// RunDreamer consolidates the last 24h of journal entries into semantic memory.
func RunDreamer(ctx context.Context, app *infra.App) (*agent.DreamerResult, error) {
	return agent.RunDreamer(ctx, app, nil)
}

// RunJanitor performs garbage collection on semantic memory.
func RunJanitor(ctx context.Context) (int, error) {
	ctx, span := infra.StartSpan(ctx, "cron.janitor")
	defer span.End()

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}

	cutoff := time.Now().AddDate(0, 0, -JanitorStaleDays)
	cutoffStr := cutoff.Format(time.RFC3339)

	iter := client.Collection(memory.KnowledgeCollection).
		Where("significance_weight", "<", JanitorWeightThreshold).
		Where("last_recalled_at", "<", cutoffStr).
		Documents(ctx)
	defer iter.Stop()

	deleted := 0
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			span.RecordError(err)
			return deleted, infra.WrapFirestoreIndexError(err)
		}

		data := doc.Data()
		nodeType := infra.GetStringField(data, "node_type")
		if nodeType == memory.NodeTypeIdentity || nodeType == memory.NodeTypeUserIdentity {
			continue // Never delete or archive identity / user_identity nodes
		}
		projectID := memory.GetLinkedCompletedProjectID(ctx, data)
		if projectID != "" {
			content := infra.GetStringField(data, "content")
			if content != "" {
				if err := memory.AppendToProjectArchiveSummary(ctx, projectID, content); err != nil {
					infra.LoggerFrom(ctx).Warn("janitor archive append failed", "project_id", projectID, "error", err)
				} else {
					infra.LoggerFrom(ctx).Debug("janitor squeezed into project", "id", doc.Ref.ID, "project_id", projectID)
				}
			}
		}

		if _, err := doc.Ref.Delete(ctx); err != nil {
			infra.LoggerFrom(ctx).Warn("janitor delete failed", "id", doc.Ref.ID, "error", err)
			continue
		}
		deleted++
		infra.LoggerFrom(ctx).Debug("janitor evicted", "id", doc.Ref.ID)
	}

	infra.LoggerFrom(ctx).Info("janitor completed", "deleted", deleted)
	span.SetAttributes(map[string]string{"deleted": fmt.Sprintf("%d", deleted)})
	return deleted, nil
}

// RunPulseAudit identifies high-value nodes that have not been recalled in PulseStaleDays and creates a proactive signal for each.
func RunPulseAudit(ctx context.Context) (*PulseResult, error) {
	ctx, span := infra.StartSpan(ctx, "cron.pulse_audit")
	defer span.End()

	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	staleThreshold := time.Now().AddDate(0, 0, -PulseStaleDays).Format(time.RFC3339)

	iter := client.Collection(memory.KnowledgeCollection).
		Where("node_type", "in", []string{"project", "goal", "person"}).
		Where("significance_weight", ">=", PulseImportanceThreshold).
		Where("last_recalled_at", "<", staleThreshold).
		Documents(ctx)
	defer iter.Stop()

	result := &PulseResult{}
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			span.RecordError(err)
			return result, infra.WrapFirestoreIndexError(err)
		}

		data := doc.Data()
		nodeID := doc.Ref.ID
		content := infra.GetStringField(data, "content")

		signalContent := fmt.Sprintf("STALE LOOP DETECTED: You haven't mentioned '%s' in 2 weeks. Is this still a priority?", content)
		_, err = memory.UpsertSemanticMemory(ctx, signalContent, "thought", "selfmodel", 0.9, []string{nodeID}, nil)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("failed to create pulse signal", "node_id", nodeID, "error", err)
			continue
		}

		result.StaleNodes = append(result.StaleNodes, nodeID)
		result.Signals++
		infra.LoggerFrom(ctx).Info("pulse audit flagged node", "id", nodeID, "content", utils.TruncateString(content, 40))
	}

	span.SetAttributes(map[string]string{
		"stale_nodes": fmt.Sprintf("%d", len(result.StaleNodes)),
		"signals":     fmt.Sprintf("%d", result.Signals),
	})
	return result, nil
}
