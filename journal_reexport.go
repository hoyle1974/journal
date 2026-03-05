package jot

import (
	"context"
	"time"

	"github.com/jackstrohm/jot/pkg/journal"
)

// Journal types re-exported from pkg/journal.
type Entry = journal.Entry
type QueryLog = journal.QueryLog
type EntryWithAnalysis = journal.EntryWithAnalysis
type JournalAnalysis = journal.JournalAnalysis
type Entity = journal.Entity
type OpenLoop = journal.OpenLoop

// Journal analysis constants.
const (
	EntityStatusPlanned    = journal.EntityStatusPlanned
	EntityStatusInProgress = journal.EntityStatusInProgress
	EntityStatusStalled    = journal.EntityStatusStalled
	EntityStatusCompleted  = journal.EntityStatusCompleted
)

// AddEntry adds a new entry to Firestore and enqueues process-entry. Returns the entry UUID.
func AddEntry(ctx context.Context, content, source string, timestamp *string) (string, error) {
	entryUUID, err := journal.AddEntry(ctx, content, source, timestamp)
	if err != nil {
		return "", err
	}
	ts := time.Now().Format(time.RFC3339)
	if timestamp != nil && *timestamp != "" {
		ts = *timestamp
	}
	payload := map[string]interface{}{
		"uuid": entryUUID, "content": content, "timestamp": ts, "source": source,
	}
	if err := EnqueueTask(ctx, "/internal/process-entry", payload); err != nil {
		LoggerFrom(ctx).Warn("failed to enqueue process-entry task, running inline", "entry_uuid", entryUUID, "error", err)
		if app := GetApp(ctx); app != nil {
			bgCtx := WithApp(context.Background(), app)
			SubmitAsync(ctx, func() {
				_ = ProcessEntry(bgCtx, entryUUID, content, ts, source)
			})
		}
	}
	return entryUUID, nil
}

func GetEntries(ctx context.Context, limit int) ([]Entry, error)           { return journal.GetEntries(ctx, limit) }
func GetEntriesAsc(ctx context.Context, limit int) ([]Entry, error)       { return journal.GetEntriesAsc(ctx, limit) }
func GetEntriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]Entry, error) {
	return journal.GetEntriesByDateRange(ctx, startDate, endDate, limit)
}
func GetEntriesWithAnalysisByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]EntryWithAnalysis, error) {
	return journal.GetEntriesWithAnalysisByDateRange(ctx, startDate, endDate, limit)
}
func SearchEntries(ctx context.Context, keywords string, limit int) ([]Entry, error) {
	return journal.SearchEntries(ctx, keywords, limit)
}
func CountEntries(ctx context.Context, startDate, endDate *string) (int, error) {
	return journal.CountEntries(ctx, startDate, endDate)
}
func GetUniqueSources(ctx context.Context) ([]string, error)               { return journal.GetUniqueSources(ctx) }
func GetEntriesBySource(ctx context.Context, sourceFilter string, limit int) ([]Entry, error) {
	return journal.GetEntriesBySource(ctx, sourceFilter, limit)
}
func GetEntry(ctx context.Context, entryUUID string) (*Entry, error)       { return journal.GetEntry(ctx, entryUUID) }
func GetEntryDates(ctx context.Context, entryIDs []string) (map[string]string, error) {
	return journal.GetEntryDates(ctx, entryIDs)
}
func UpdateEntry(ctx context.Context, entryUUID, newContent string) error {
	return journal.UpdateEntry(ctx, entryUUID, newContent)
}
func DeleteEntry(ctx context.Context, entryUUID string) error              { return journal.DeleteEntry(ctx, entryUUID) }
func DeleteEntries(ctx context.Context, entryUUIDs []string) error         { return journal.DeleteEntries(ctx, entryUUIDs) }
func GetDatesWithEntries(ctx context.Context) ([]string, error)            { return journal.GetDatesWithEntries(ctx) }
func QuerySimilarEntries(ctx context.Context, queryVector []float32, limit int) ([]Entry, error) {
	return journal.QuerySimilarEntries(ctx, queryVector, limit)
}
func BackfillEntryEmbeddings(ctx context.Context, limit int) (int, error) {
	return journal.BackfillEntryEmbeddings(ctx, limit)
}

func SaveQuery(ctx context.Context, question, answer, source string, isGap bool) (string, error) {
	return journal.SaveQuery(ctx, question, answer, source, isGap)
}
func GetRecentQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	return journal.GetRecentQueries(ctx, limit)
}
func SearchQueries(ctx context.Context, keywords string, limit int) ([]QueryLog, error) {
	return journal.SearchQueries(ctx, keywords, limit)
}
func GetRecentGapQueries(ctx context.Context, limit int) ([]QueryLog, error) {
	return journal.GetRecentGapQueries(ctx, limit)
}
func GetQueriesByDateRange(ctx context.Context, startDate, endDate string, limit int) ([]QueryLog, error) {
	return journal.GetQueriesByDateRange(ctx, startDate, endDate, limit)
}

func AnalyzeJournalEntry(ctx context.Context, entryContent, entryUUID, entryTimestamp string) (*JournalAnalysis, error) {
	return journal.AnalyzeJournalEntry(ctx, entryContent, entryUUID, entryTimestamp)
}
func NormalizeEntityStatus(s string) string { return journal.NormalizeEntityStatus(s) }

// Format and display helpers (from pkg/journal and pkg/utils).
const (
	DateDisplayLen     = journal.DateDisplayLen
	DateTimeDisplayLen = journal.DateTimeDisplayLen
)

func TruncateTimestamp(ts string, maxLen int) string { return journal.TruncateTimestamp(ts, maxLen) }
func FormatEntriesForContext(entries []Entry, maxChars int) string {
	return journal.FormatEntriesForContext(entries, maxChars)
}
func FormatQueriesForContext(queries []QueryLog, maxChars int) string {
	return journal.FormatQueriesForContext(queries, maxChars)
}

// ToolResult represents the result of executing a tool (for compatibility).
type ToolResult struct {
	Success bool
	Result  string
}
