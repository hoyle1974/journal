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
	// Reuse logToGDocSync which already applies bold formatting to everything it writes.
	// Wrap the narrative with a newline so it is on its own paragraph.
	logToGDocSync(ctx, cfg, narrative)
	infra.LoggerFrom(ctx).Debug("debug report written to gdoc", "length", len(narrative))
}
