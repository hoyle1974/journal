package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/api/iterator"
)

// EvictStaleNodes performs janitor garbage collection: deletes low-significance nodes
// that have not been recalled since the given stale cutoff. Nodes of type identity_anchor
// or user_identity are never deleted. Content from nodes linked to completed projects
// is appended to the project's archive_summary before deletion.
// weightThreshold is the upper bound for significance_weight (e.g. 0.2); staleDays is the age in days.
func EvictStaleNodes(ctx context.Context, weightThreshold float64, staleDays int) (int, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().AddDate(0, 0, -staleDays)
	cutoffStr := cutoff.Format(time.RFC3339)

	iter := client.Collection(KnowledgeCollection).
		Where("significance_weight", "<", weightThreshold).
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
			return deleted, infra.WrapFirestoreIndexError(err)
		}

		data := doc.Data()
		nodeType := infra.GetStringField(data, "node_type")
		if nodeType == NodeTypeIdentity || nodeType == NodeTypeUserIdentity {
			continue
		}
		projectID := GetLinkedCompletedProjectID(ctx, data)
		if projectID != "" {
			content := infra.GetStringField(data, "content")
			if content != "" {
				if err := AppendToProjectArchiveSummary(ctx, projectID, content); err != nil {
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

	return deleted, nil
}

// PulseAuditResult is the outcome of CreatePulseAuditSignals.
type PulseAuditResult struct {
	StaleNodes []string
	Signals    int
}

// CreatePulseAuditSignals finds high-value project/goal/person nodes that have not been
// recalled since the stale cutoff and creates a proactive "stale loop" signal for each.
// importanceThreshold is the lower bound for significance_weight (e.g. 0.7); staleDays is the age in days.
func CreatePulseAuditSignals(ctx context.Context, importanceThreshold float64, staleDays int) (*PulseAuditResult, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}

	staleThreshold := time.Now().AddDate(0, 0, -staleDays).Format(time.RFC3339)

	iter := client.Collection(KnowledgeCollection).
		Where("node_type", "in", []string{"project", "goal", "person"}).
		Where("significance_weight", ">=", importanceThreshold).
		Where("last_recalled_at", "<", staleThreshold).
		Documents(ctx)
	defer iter.Stop()

	result := &PulseAuditResult{}
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return result, infra.WrapFirestoreIndexError(err)
		}

		data := doc.Data()
		nodeID := doc.Ref.ID
		content := infra.GetStringField(data, "content")

		signalContent := fmt.Sprintf("STALE LOOP DETECTED: You haven't mentioned '%s' in 2 weeks. Is this still a priority?", content)
		_, err = UpsertSemanticMemory(ctx, signalContent, "thought", "selfmodel", 0.9, []string{nodeID}, nil)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("failed to create pulse signal", "node_id", nodeID, "error", err)
			continue
		}

		result.StaleNodes = append(result.StaleNodes, nodeID)
		result.Signals++
		infra.LoggerFrom(ctx).Info("pulse audit flagged node", "id", nodeID, "content", utils.TruncateString(content, 40))
	}

	return result, nil
}
