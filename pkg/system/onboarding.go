package system

import (
	"context"
	"fmt"

	"github.com/jackstrohm/jot/internal/infra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// OnboardingDocExists returns true if _system/onboarding document exists.
func OnboardingDocExists(ctx context.Context, app FirestoreProvider) (bool, error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return false, fmt.Errorf("onboarding firestore client: %w", err)
	}
	ref := client.Collection(infra.SystemCollection).Doc(infra.OnboardingDoc)
	doc, err := ref.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}
		return false, fmt.Errorf("onboarding check: %w", err)
	}
	return doc.Exists(), nil
}

// SetOnboardingComplete writes status, seeded_at, and version to _system/onboarding.
func SetOnboardingComplete(ctx context.Context, app FirestoreProvider, statusVal string, seededAt string, version int) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("onboarding firestore: %w", err)
	}
	ref := client.Collection(infra.SystemCollection).Doc(infra.OnboardingDoc)
	_, err = ref.Set(ctx, map[string]interface{}{
		"status":    statusVal,
		"seeded_at": seededAt,
		"version":   version,
	})
	if err != nil {
		return fmt.Errorf("onboarding mark complete: %w", err)
	}
	return nil
}
