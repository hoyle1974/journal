package service

import (
	"context"

	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/memory"
)

// MemoryService handles memory and knowledge operations for the API.
type MemoryService struct{}

// NewMemoryService returns a MemoryService.
func NewMemoryService() *MemoryService {
	return &MemoryService{}
}

// InitializePermanentContexts ensures permanent context nodes exist.
func (m *MemoryService) InitializePermanentContexts(ctx context.Context) error {
	return memory.InitializePermanentContexts(ctx)
}

// DecayContexts decays stale context weights.
func (m *MemoryService) DecayContexts(ctx context.Context) (int, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "DecayContexts")
	count, err := memory.DecayContexts(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "DecayContexts", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "DecayContexts", "decayed_count", count)
	return count, nil
}

// GetUnresolvedPendingQuestions returns pending questions for the API (api type).
func (m *MemoryService) GetUnresolvedPendingQuestions(ctx context.Context, limit int) ([]api.PendingQuestion, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "GetUnresolvedPendingQuestions", "limit", limit)
	qs, err := memory.GetUnresolvedPendingQuestions(ctx, limit)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "GetUnresolvedPendingQuestions", "error", err.Error())
		return nil, err
	}
	out := make([]api.PendingQuestion, len(qs))
	for i := range qs {
		out[i] = api.PendingQuestion{
			UUID:           qs[i].UUID,
			Question:       qs[i].Question,
			Kind:           qs[i].Kind,
			Context:        qs[i].Context,
			SourceEntryIDs: qs[i].SourceEntryIDs,
			CreatedAt:      qs[i].CreatedAt,
		}
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "GetUnresolvedPendingQuestions", "count", len(out))
	return out, nil
}

// ResolvePendingQuestion marks a question resolved.
func (m *MemoryService) ResolvePendingQuestion(ctx context.Context, id, answer string) error {
	infra.LoggerFrom(ctx).Info("function call", "fn", "ResolvePendingQuestion", "id", id, "answer_length", len(answer))
	if err := memory.ResolvePendingQuestion(ctx, id, answer); err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "ResolvePendingQuestion", "id", id, "error", err.Error())
		return err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "ResolvePendingQuestion", "id", id)
	return nil
}

// Ensure MemoryService implements api.MemoryService.
var _ api.MemoryService = (*MemoryService)(nil)