package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
)

// IngestQuestionAnswer persists a resolved gap question as a journal entry and runs
// the refinery pipeline on it so the answer is indexed in the knowledge graph.
// The entry is stored with source "dreamer" to signal it came from the background loop.
// This function is synchronous; callers that want async must wrap it in a goroutine.
func IngestQuestionAnswer(ctx context.Context, app *infra.App, q memory.PendingQuestion) {
	log := infra.LoggerFrom(ctx)
	if app == nil {
		log.Warn("question ingest: app is nil, skipping")
		return
	}
	if q.Kind != "gap" || q.Answer == "" {
		return
	}

	text := fmt.Sprintf("Jot asked: %s\nUser responded: %s", q.Question, q.Answer)
	ts := time.Now().Format(time.RFC3339)

	entryUUID, err := AddEntryOnly(ctx, app, text, "dreamer", &ts, "")
	if err != nil {
		log.Warn("question ingest: failed to add entry", "question_uuid", q.UUID, "error", err)
		return
	}
	log.Info("question ingest: entry created", "question_uuid", q.UUID, "entry_uuid", entryUUID)

	if _, err := ProcessEntrySyncPipeline(ctx, app, entryUUID, text, "dreamer"); err != nil {
		log.Warn("question ingest: refinery pipeline failed", "question_uuid", q.UUID, "entry_uuid", entryUUID, "error", err)
	}
}
