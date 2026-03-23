package api

import (
	"context"
	"strings"
)

const telegramHelpText = `Available commands:

/help — Show this message
/skip — Skip the current pending clarification question`

// handleTelegramSlashCommand dispatches known slash commands. Returns (true, err) when the command
// was handled (caller should return early). Returns (false, nil) for unknown commands, which
// fall through to FOH processing.
func handleTelegramSlashCommand(ctx context.Context, s *Server, chatID int64, text string) (bool, error) {
	cmd := strings.ToLower(strings.Fields(text)[0])
	switch cmd {
	case "/help":
		return true, handleTelegramHelp(ctx, s, chatID)
	}
	return false, nil
}

// handleTelegramHelp sends the list of available slash commands.
func handleTelegramHelp(ctx context.Context, s *Server, chatID int64) error {
	return s.Telegram.SendMessage(ctx, chatID, telegramHelpText)
}
