package system

import (
	"context"
	"fmt"

	"github.com/jackstrohm/jot/internal/infra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DreamMeta holds the Dreamer background cycle watermark stored at _system/dream_meta.
type DreamMeta struct {
	// LastProcessedAt is the RFC3339 timestamp of the most recent log entry included
	// in the last successful dream cycle. Entries with timestamp > LastProcessedAt
	// are candidates for the next cycle.
	LastProcessedAt string `firestore:"last_processed_at"`
	// Version is incremented when the dream cycle schema changes.
	Version int `firestore:"version"`
}

// GetDreamMeta reads _system/dream_meta. Returns a zero-value DreamMeta when the
// document does not exist yet (first run).
func GetDreamMeta(ctx context.Context, app FirestoreProvider) (DreamMeta, error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return DreamMeta{}, fmt.Errorf("dream_meta: firestore client: %w", err)
	}
	doc, err := client.Collection(infra.SystemCollection).Doc(infra.DreamMetaDoc).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return DreamMeta{}, nil
		}
		return DreamMeta{}, fmt.Errorf("dream_meta: get: %w", err)
	}
	var meta DreamMeta
	if err := doc.DataTo(&meta); err != nil {
		return DreamMeta{}, fmt.Errorf("dream_meta: decode: %w", err)
	}
	return meta, nil
}

// SetDreamMeta writes the Dreamer watermark to _system/dream_meta.
func SetDreamMeta(ctx context.Context, app FirestoreProvider, meta DreamMeta) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("dream_meta: firestore client: %w", err)
	}
	_, err = client.Collection(infra.SystemCollection).Doc(infra.DreamMetaDoc).Set(ctx, map[string]any{
		"last_processed_at": meta.LastProcessedAt,
		"version":           meta.Version,
	})
	if err != nil {
		return fmt.Errorf("dream_meta: set: %w", err)
	}
	return nil
}
