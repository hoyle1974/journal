package jot

import (
	"context"
)

type currentEntryUUIDKeyType struct{}

var currentEntryUUIDKey = &currentEntryUUIDKeyType{}

// WithCurrentEntryUUID returns a context that carries the current journal entry UUID (e.g. the query that triggered FOH).
// Used so tools like upsert_knowledge can link new facts to their source entry.
func WithCurrentEntryUUID(ctx context.Context, entryUUID string) context.Context {
	return context.WithValue(ctx, currentEntryUUIDKey, entryUUID)
}

// CurrentEntryUUIDFrom returns the current entry UUID from context, or "" if not set.
func CurrentEntryUUIDFrom(ctx context.Context) string {
	if s, ok := ctx.Value(currentEntryUUIDKey).(string); ok && s != "" {
		return s
	}
	return ""
}
