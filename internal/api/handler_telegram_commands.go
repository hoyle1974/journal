package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
)

const telegramHelpText = `Available commands:

/help — Show this message
/skip — Skip the current pending clarification question
/dream — Run the dream cycle to synthesise recent activity`

// handleTelegramSlashCommand dispatches known slash commands. Returns (true, err) when the command
// was handled (caller should return early). Returns (false, nil) for unknown commands, which
// fall through to FOH processing.
func handleTelegramSlashCommand(ctx context.Context, s *Server, chatID int64, text string) (bool, error) {
	cmd := strings.ToLower(strings.Fields(text)[0])
	switch cmd {
	case "/help":
		return true, handleTelegramHelp(ctx, s, chatID)
	case "/dream":
		return true, handleTelegramDream(ctx, s, chatID)
	}
	return false, nil
}

// handleTelegramHelp sends the list of available slash commands.
func handleTelegramHelp(ctx context.Context, s *Server, chatID int64) error {
	return s.Telegram.SendMessage(ctx, chatID, telegramHelpText)
}

// handleTelegramDream triggers the Dreamer background cycle and reports the result.
func handleTelegramDream(ctx context.Context, s *Server, chatID int64) error {
	result, err := s.Agent.RunDreamer(ctx, true)
	if err != nil {
		infra.LoggerFrom(ctx).Error("handleTelegramDream: dream cycle failed", "error", err)
		_ = s.Telegram.SendMessage(ctx, chatID, "Dream cycle failed. Check logs for details.")
		return err
	}
	if result.Skipped {
		return s.Telegram.SendMessage(ctx, chatID, fmt.Sprintf("Dream cycle skipped: %s", result.SkipReason))
	}
	msg := fmt.Sprintf("Dream cycle complete.\nSummary saved: %s\nQuestions enqueued: %d",
		result.SummaryUUID, len(result.Questions))
	return s.Telegram.SendMessage(ctx, chatID, msg)
}
