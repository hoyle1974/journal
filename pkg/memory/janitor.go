package memory

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/api/iterator"
)

// EvictStaleNodes performs janitor garbage collection: deletes low-significance nodes
// that have not been recalled since the given stale cutoff. Nodes of type identity_anchor,
// user_identity, or log (episodic journal entries) are never deleted. Content from nodes
// linked to completed projects is appended to the project's archive_summary before deletion.
// weightThreshold is the upper bound for significance_weight (e.g. 0.2); staleDays is the age in days.
func (s *Store) EvictStaleNodes(ctx context.Context, weightThreshold float64, staleDays int) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -staleDays)
	cutoffStr := cutoff.Format(time.RFC3339)

	iter := s.db.Collection(KnowledgeCollection).
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
			return deleted, wrapFirestoreIndexError(err)
		}

		data := doc.Data()
		nodeType := getStringField(data, "node_type")
		// Never delete protected node types: identity anchors, user identity, or raw log entries.
		if nodeType == NodeTypeIdentity || nodeType == NodeTypeUserIdentity || nodeType == "log" {
			continue
		}
		projectID := s.GetLinkedCompletedProjectID(ctx, data)
		if projectID != "" {
			content := getStringField(data, "content")
			if content != "" {
				if err := s.AppendToProjectArchiveSummary(ctx, projectID, content); err != nil {
					s.log.Warn("janitor archive append failed", "project_id", projectID, "error", err)
				} else {
					s.log.Debug("janitor squeezed into project", "id", doc.Ref.ID, "project_id", projectID)
				}
			}
		}

		if _, err := doc.Ref.Delete(ctx); err != nil {
			s.log.Warn("janitor delete failed", "id", doc.Ref.ID, "error", err)
			continue
		}
		deleted++
		s.log.Debug("janitor evicted", "id", doc.Ref.ID)
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
func (s *Store) CreatePulseAuditSignals(ctx context.Context, importanceThreshold float64, staleDays int) (*PulseAuditResult, error) {
	staleThreshold := time.Now().AddDate(0, 0, -staleDays).Format(time.RFC3339)

	iter := s.db.Collection(KnowledgeCollection).
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
			return result, wrapFirestoreIndexError(err)
		}

		data := doc.Data()
		nodeID := doc.Ref.ID
		content := getStringField(data, "content")

		signalContent := fmt.Sprintf("STALE LOOP DETECTED: You haven't mentioned '%s' in 2 weeks. Is this still a priority?", content)
		_, err = s.UpsertSemanticMemory(ctx, signalContent, "thought", "selfmodel", 0.9, []string{nodeID}, nil)
		if err != nil {
			s.log.Warn("failed to create pulse signal", "node_id", nodeID, "error", err)
			continue
		}

		result.StaleNodes = append(result.StaleNodes, nodeID)
		result.Signals++
		s.log.Info("pulse audit flagged node", "id", nodeID, "content", truncateString(content, 40))
	}

	return result, nil
}
