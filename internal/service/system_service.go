package service

import (
	"context"

	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/system"
)

// SystemService wraps pkg/system and satisfies api.SystemService for the API layer.
type SystemService struct {
	app *infra.App
}

// NewSystemService returns a SystemService that delegates to pkg/system using the given app.
func NewSystemService(app *infra.App) *SystemService {
	return &SystemService{app: app}
}

// Ensure SystemService implements api.SystemService.
var _ api.SystemService = (*SystemService)(nil)

func (s *SystemService) OnboardingDocExists(ctx context.Context) (bool, error) {
	return system.OnboardingDocExists(ctx, s.app)
}

func (s *SystemService) SetOnboardingComplete(ctx context.Context, statusVal, seededAt string, version int) error {
	return system.SetOnboardingComplete(ctx, s.app, statusVal, seededAt, version)
}
