package system

import (
	"context"
	"fmt"

	"github.com/jackstrohm/jot/internal/infra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// BriefingMeta holds the Morning Briefing watermark stored at _system/briefing_meta.
type BriefingMeta struct {
	// LastProcessedAt is the RFC3339 timestamp of the most recent log entry included
	// in the last successful briefing. Entries with timestamp > LastProcessedAt are
	// candidates for the next briefing.
	LastProcessedAt string `firestore:"last_processed_at"`
	// Version is incremented when the briefing schema changes.
	Version int `firestore:"version"`
}

// GetBriefingMeta reads _system/briefing_meta. Returns a zero-value BriefingMeta when the
// document does not exist yet (first run).
func GetBriefingMeta(ctx context.Context, app FirestoreProvider) (BriefingMeta, error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return BriefingMeta{}, fmt.Errorf("briefing_meta: firestore client: %w", err)
	}
	doc, err := client.Collection(infra.SystemCollection).Doc(infra.BriefingMetaDoc).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return BriefingMeta{}, nil
		}
		return BriefingMeta{}, fmt.Errorf("briefing_meta: get: %w", err)
	}
	var meta BriefingMeta
	if err := doc.DataTo(&meta); err != nil {
		return BriefingMeta{}, fmt.Errorf("briefing_meta: decode: %w", err)
	}
	return meta, nil
}

// SetBriefingMeta writes the Morning Briefing watermark to _system/briefing_meta.
func SetBriefingMeta(ctx context.Context, app FirestoreProvider, meta BriefingMeta) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("briefing_meta: firestore client: %w", err)
	}
	_, err = client.Collection(infra.SystemCollection).Doc(infra.BriefingMetaDoc).Set(ctx, map[string]any{
		"last_processed_at": meta.LastProcessedAt,
		"version":           meta.Version,
	})
	if err != nil {
		return fmt.Errorf("briefing_meta: set: %w", err)
	}
	return nil
}
