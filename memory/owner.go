package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/api/iterator"
)

// FetchOwnerName returns the primary name of the user from the identity_anchor node.
// Returns ("", nil) when no identity anchor has been established yet.
func (s *Store) FetchOwnerName(ctx context.Context) (string, error) {
	iter := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeIdentity).
		Limit(1).
		Documents(ctx)
	doc, err := iter.Next()
	iter.Stop()
	if err == iterator.Done {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("fetch owner name: %w", err)
	}
	return getStringField(doc.Data(), "content"), nil
}

// UpsertOwnerName creates or updates the singleton identity_anchor node with the given name.
// This is idempotent: calling it multiple times with the same name is safe.
func (s *Store) UpsertOwnerName(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	docID := stableEntityDocID(NodeTypeIdentity, "owner")
	_, err := s.db.Collection(KnowledgeCollection).Doc(docID).Set(ctx, map[string]any{
		"content":   name,
		"node_type": NodeTypeIdentity,
		"metadata":  fmt.Sprintf(`{"primary_name":%q}`, name),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("upsert owner name: %w", err)
	}
	s.log.Info("identity anchor set", "owner_name", name)
	return nil
}
