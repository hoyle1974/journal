// Package memory — project and goal node status management and archive helpers.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/utils"
)

// UpdateProjectStatus sets the status field on a project or goal node's metadata.
// The node must exist and have node_type "project" or "goal"; status is validated against the project/goal schema.
func (s *Store) UpdateProjectStatus(ctx context.Context, nodeID, status string) error {
	node, err := s.GetKnowledgeNodeByID(ctx, nodeID)
	if err != nil {
		return err
	}
	if node == nil {
		return fmt.Errorf("update project status %q: %w", nodeID, ErrNotFound)
	}
	if node.NodeType != NodeTypeProject && node.NodeType != NodeTypeGoal {
		return fmt.Errorf("node %q is not a project or goal (node_type=%q)", nodeID, node.NodeType)
	}

	var meta map[string]any
	if node.Metadata != "" {
		_ = json.Unmarshal([]byte(node.Metadata), &meta)
	}
	if meta == nil {
		meta = make(map[string]any)
	}
	meta["status"] = strings.ToLower(strings.TrimSpace(status))
	if err := ValidateMetadata(node.NodeType, meta); err != nil {
		return fmt.Errorf("invalid status: %w", err)
	}
	normalized, err := NormalizeMetadata(node.NodeType, meta)
	if err != nil {
		return fmt.Errorf("normalize metadata: %w", err)
	}
	metaJSON, err := MetadataToJSON(normalized)
	if err != nil {
		return err
	}

	_, err = s.db.Collection(KnowledgeCollection).Doc(nodeID).Update(ctx, []firestore.Update{
		{Path: "metadata", Value: metaJSON},
	})
	return err
}

func metadataStatus(metadata string) string {
	if metadata == "" {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(metadata), &m); err != nil {
		return ""
	}
	s, _ := m["status"].(string)
	return s
}

// AppendToProjectArchiveSummary appends a one-line summary to a project/goal node's archive_summary in metadata.
func (s *Store) AppendToProjectArchiveSummary(ctx context.Context, projectID, oneLine string) error {
	doc, err := s.db.Collection(KnowledgeCollection).Doc(projectID).Get(ctx)
	if err != nil {
		return err
	}
	data := doc.Data()
	metadataStr := getStringField(data, "metadata")
	var meta map[string]interface{}
	if metadataStr != "" {
		_ = json.Unmarshal([]byte(metadataStr), &meta)
	}
	if meta == nil {
		meta = make(map[string]interface{})
	}
	current, _ := meta["archive_summary"].(string)
	if current == "" {
		current = getStringField(data, "archive_summary")
	}
	line := oneLine
	if len(line) > 200 {
		line = utils.TruncateToMaxBytes(line, 197) + "..."
	}
	if current != "" {
		current += "\n- " + line
	} else {
		current = "- " + line
	}
	meta["archive_summary"] = current
	updatedMetadata, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	_, err = s.db.Collection(KnowledgeCollection).Doc(projectID).Update(ctx, []firestore.Update{
		{Path: "metadata", Value: string(updatedMetadata)},
	})
	return err
}

// GetLinkedCompletedProjectID returns the ID of a completed project linked to this node, or "".
func (s *Store) GetLinkedCompletedProjectID(ctx context.Context, nodeData map[string]interface{}) string {
	metadataStr := getStringField(nodeData, "metadata")
	var meta map[string]interface{}
	if metadataStr != "" {
		_ = json.Unmarshal([]byte(metadataStr), &meta)
	}
	if meta != nil {
		if pid, ok := meta["parent_goal"].(string); ok && pid != "" {
			if s.isCompletedProjectByID(ctx, pid) {
				return pid
			}
		}
		if pid, ok := meta["project_uuid"].(string); ok && pid != "" {
			if s.isCompletedProjectByID(ctx, pid) {
				return pid
			}
		}
	}
	for _, id := range getStringSliceField(nodeData, "entity_links") {
		if s.isCompletedProjectByID(ctx, id) {
			return id
		}
	}
	return ""
}

func (s *Store) isCompletedProjectByID(ctx context.Context, id string) bool {
	node, err := s.GetKnowledgeNodeByID(ctx, id)
	if err != nil || node == nil {
		return false
	}
	return (node.NodeType == "project" || node.NodeType == "goal") && metadataStatus(node.Metadata) == "completed"
}

// FindProjectOrGoalByName finds the nearest project or goal knowledge node by semantic similarity to the given name.
func (s *Store) FindProjectOrGoalByName(ctx context.Context, projectName string) (*KnowledgeNode, error) {
	vec, err := s.embedder.GenerateEmbedding(ctx, "Project: "+projectName, EmbedTaskRetrievalQuery)
	if err != nil {
		return nil, err
	}
	nodes, err := s.QuerySimilarNodes(ctx, vec, 5)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		if nodes[i].NodeType == NodeTypeProject || nodes[i].NodeType == NodeTypeGoal {
			return &nodes[i], nil
		}
	}
	return nil, nil
}
