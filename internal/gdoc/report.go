package gdoc

import (
	"context"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/infra"
)

// WriteReport writes a debug narrative report directly to the Google Doc [LOGS] section as bold text.
// Unlike the standard log line, the narrative is written as-is (no timestamp prefix) because the
// narrative is self-contained and timestamped internally by context.
// Errors are logged and swallowed so callers can degrade gracefully.
func WriteReport(ctx context.Context, cfg *config.Config, narrative string) {
	if cfg == nil || narrative == "" {
		return
	}
	if cfg.DocumentID == "" {
		infra.LoggerFrom(ctx).Warn("debug report skipped: DOCUMENT_ID not configured")
		return
	}
	// logToGDocSync applies bold formatting, appends a trailing newline, and logs success/failure.
	logToGDocSync(ctx, cfg, narrative)
}
