package service

import (
	"context"

	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/utils"
)

// JournalService handles journal and entry operations for the API.
type JournalService struct{}

// NewJournalService returns a JournalService for use with api.Server.
func NewJournalService() *JournalService {
	return &JournalService{}
}

// SaveQuery saves a Q&A to the journal. Exposed for api handlers.
func (j *JournalService) SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "SaveQuery", "source", source, "is_gap", isGap, "question_preview", utils.TruncateString(question, 60))
	id, err := journal.SaveQuery(ctx, question, answer, source, isGap)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "SaveQuery", "error", err.Error())
		return "", err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "SaveQuery", "id", id)
	return id, nil
}

// GetEntry returns a single entry by UUID.
func (j *JournalService) GetEntry(ctx context.Context, uuid string) (*api.Entry, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "GetEntry", "uuid", uuid)
	entry, err := journal.GetEntry(ctx, uuid)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("function result", "fn", "GetEntry", "uuid", uuid, "error", err.Error())
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "GetEntry", "uuid", uuid, "found", true)
	return journalEntryToAPI(entry), nil
}

// GetEntries returns recent entries up to limit.
func (j *JournalService) GetEntries(ctx context.Context, limit int) ([]api.Entry, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "GetEntries", "limit", limit)
	entries, err := journal.GetEntries(ctx, limit)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "GetEntries", "error", err.Error())
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "GetEntries", "count", len(entries))
	out := make([]api.Entry, len(entries))
	for i := range entries {
		out[i] = *journalEntryToAPI(&entries[i])
	}
	return out, nil
}

func journalEntryToAPI(e *journal.Entry) *api.Entry {
	if e == nil {
		return nil
	}
	return &api.Entry{UUID: e.UUID, Content: e.Content, Source: e.Source, Timestamp: e.Timestamp}
}

// UpdateEntry updates an entry's content.
func (j *JournalService) UpdateEntry(ctx context.Context, uuid, content string) error {
	infra.LoggerFrom(ctx).Info("function call", "fn", "UpdateEntry", "uuid", uuid, "content_length", len(content))
	err := journal.UpdateEntry(ctx, uuid, content)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "UpdateEntry", "uuid", uuid, "error", err.Error())
		return err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "UpdateEntry", "uuid", uuid)
	return nil
}

// DeleteEntries deletes entries by UUIDs.
func (j *JournalService) DeleteEntries(ctx context.Context, uuids []string) error {
	infra.LoggerFrom(ctx).Info("function call", "fn", "DeleteEntries", "uuid_count", len(uuids))
	err := journal.DeleteEntries(ctx, uuids)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "DeleteEntries", "error", err.Error())
		return err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "DeleteEntries", "deleted", len(uuids))
	return nil
}

// BackfillEntryEmbeddings backfills embeddings for entries that lack them.
func (j *JournalService) BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "BackfillEntryEmbeddings", "limit", limit)
	processed, err := journal.BackfillEntryEmbeddings(ctx, limit)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "BackfillEntryEmbeddings", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "BackfillEntryEmbeddings", "processed", processed)
	return processed, nil
}
