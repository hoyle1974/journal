package service

import (
	"context"
	"time"

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

func (s *SystemService) AcquireSyncLock(ctx context.Context) (bool, error) {
	return system.AcquireSyncLock(ctx, s.app)
}

func (s *SystemService) ReleaseSyncLock(ctx context.Context) {
	system.ReleaseSyncLock(ctx, s.app)
}

func (s *SystemService) GetSyncStateLastBlockHash(ctx context.Context) (string, bool, error) {
	return system.GetSyncStateLastBlockHash(ctx, s.app)
}

func (s *SystemService) SetSyncStateAfterProcess(ctx context.Context, blockHash string) error {
	return system.SetSyncStateAfterProcess(ctx, s.app, blockHash)
}

func (s *SystemService) GetDebounceState(ctx context.Context) (string, error) {
	return system.GetDebounceState(ctx, s.app)
}

func (s *SystemService) SetDebounceState(ctx context.Context, taskName string, scheduledTime time.Time) error {
	return system.SetDebounceState(ctx, s.app, taskName, scheduledTime)
}

func (s *SystemService) GetLatestDream(ctx context.Context) (*system.LatestDream, error) {
	return system.GetLatestDream(ctx, s.app)
}

func (s *SystemService) MarkLatestDreamRead(ctx context.Context) error {
	return system.MarkLatestDreamRead(ctx, s.app)
}

func (s *SystemService) WriteLatestDream(ctx context.Context, narrative, timestamp string, unread bool) error {
	return system.WriteLatestDream(ctx, s.app, narrative, timestamp, unread)
}

func (s *SystemService) GetDreamRunState(ctx context.Context) (*system.DreamRunState, error) {
	return system.GetDreamRunState(ctx, s.app)
}

func (s *SystemService) TryAcquireDreamRunLock(ctx context.Context, runID string) (bool, string, error) {
	return system.TryAcquireDreamRunLock(ctx, s.app, runID)
}

func (s *SystemService) UpdateDreamRunPhase(ctx context.Context, runID, phase, logLine string) error {
	return system.UpdateDreamRunPhase(ctx, s.app, runID, phase, logLine)
}

func (s *SystemService) SetDreamRunCompleted(ctx context.Context, runID string, result map[string]interface{}) error {
	return system.SetDreamRunCompleted(ctx, s.app, runID, result)
}

func (s *SystemService) SetDreamRunFailed(ctx context.Context, runID string, errMsg string) error {
	return system.SetDreamRunFailed(ctx, s.app, runID, errMsg)
}

func (s *SystemService) AppendDreamRunLog(ctx context.Context, runID string, logLine string) error {
	return system.AppendDreamRunLog(ctx, s.app, runID, logLine)
}

func (s *SystemService) OnboardingDocExists(ctx context.Context) (bool, error) {
	return system.OnboardingDocExists(ctx, s.app)
}

func (s *SystemService) SetOnboardingComplete(ctx context.Context, statusVal, seededAt string, version int) error {
	return system.SetOnboardingComplete(ctx, s.app, statusVal, seededAt, version)
}
