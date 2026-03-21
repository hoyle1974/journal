package service

import (
	"context"
	"errors"

	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

// JournalService handles journal and entry operations for the API.
type JournalService struct {
	env  infra.ToolEnv
	cfg  *config.Config
}

// NewJournalService returns a JournalService that uses the given ToolEnv and optional config (required for BackfillEntryEmbeddings).
func NewJournalService(env infra.ToolEnv, cfg *config.Config) *JournalService {
	return &JournalService{env: env, cfg: cfg}
}

// SaveQuery saves a Q&A to the journal. Exposed for api handlers.
func (j *JournalService) SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	infra.LoggerFrom(ctx).Info("function call", "fn", "SaveQuery", "source", source, "is_gap", isGap, "question_preview", utils.TruncateString(question, 60))
	id, err := j.env.MemoryStore().SaveQuery(ctx, question, answer, source, isGap)
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
	entry, err := j.env.MemoryStore().GetEntry(ctx, uuid)
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
	entries, err := j.env.MemoryStore().GetEntries(ctx, limit)
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

func journalEntryToAPI(e *memory.Entry) *api.Entry {
	if e == nil {
		return nil
	}
	return &api.Entry{UUID: e.UUID, Content: e.Content, Source: e.Source, Timestamp: e.Timestamp, ImageURL: e.ImageURL}
}

// UpdateEntry updates an entry's content.
func (j *JournalService) UpdateEntry(ctx context.Context, uuid, content string) error {
	infra.LoggerFrom(ctx).Info("function call", "fn", "UpdateEntry", "uuid", uuid, "content_length", len(content))
	err := j.env.MemoryStore().UpdateEntry(ctx, uuid, content)
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
	err := j.env.MemoryStore().DeleteEntries(ctx, uuids)
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
	if j.cfg == nil {
		return 0, errors.New("config required for backfill")
	}
	processed, err := j.env.MemoryStore().BackfillEntryEmbeddings(ctx, limit)
	if err != nil {
		infra.LoggerFrom(ctx).Error("function result", "fn", "BackfillEntryEmbeddings", "error", err.Error())
		return 0, err
	}
	infra.LoggerFrom(ctx).Info("function result", "fn", "BackfillEntryEmbeddings", "processed", processed)
	return processed, nil
}
