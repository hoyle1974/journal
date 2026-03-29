package memory

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
)

// EmbedEntryMedia generates a multimodal embedding from raw bytes and stores it on the entry
// document. This is used for image and audio entries where the primary retrieval vector should
// capture the raw modality rather than a text description.
//
// uuid is the journal document ID. bytes must be non-empty. mimeType is required (e.g. "image/jpeg").
func (s *Store) EmbedEntryMedia(ctx context.Context, uuid string, bytes []byte, mimeType string) error {
	if len(bytes) == 0 {
		return fmt.Errorf("EmbedEntryMedia: bytes must not be empty")
	}
	if s.embedder == nil {
		return fmt.Errorf("EmbedEntryMedia: embedder is nil")
	}
	vec, err := s.embedder.EmbedContent(ctx, []EmbedPart{{Bytes: bytes, MIMEType: mimeType}})
	if err != nil {
		return fmt.Errorf("EmbedEntryMedia: embed: %w", err)
	}
	_, err = s.db.Collection(KnowledgeCollection).Doc(uuid).Update(ctx, []firestore.Update{
		{Path: "embedding", Value: firestore.Vector32(vec)},
	})
	if err != nil {
		return fmt.Errorf("EmbedEntryMedia: update: %w", err)
	}
	return nil
}
