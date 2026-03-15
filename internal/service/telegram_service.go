package service

import (
	"context"
	"net/http"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/telegram"
)

// TelegramService handles Telegram Bot API operations for the API.
type TelegramService struct {
	getConfig ConfigGetter
}

// NewTelegramService returns a TelegramService. getConfig is for Telegram/config. Callers pass app to ProcessIncomingTelegram.
func NewTelegramService(getConfig ConfigGetter) *TelegramService {
	return &TelegramService{getConfig: getConfig}
}

func (s *TelegramService) cfg() *config.Config {
	if s.getConfig != nil {
		return s.getConfig()
	}
	return nil
}

// ValidateSecretToken validates the Telegram webhook secret token header.
func (s *TelegramService) ValidateSecretToken(r *http.Request) bool {
	return telegram.ValidateSecretToken(s.cfg(), r, infra.LoggerFrom(r.Context()))
}

// ParseWebhook parses the Telegram webhook request body.
func (s *TelegramService) ParseWebhook(r *http.Request) (*telegram.WebhookUpdate, *telegram.IncomingMessage, error) {
	return telegram.ParseWebhook(r)
}

// IsAllowedUser returns whether the Telegram user ID is allowed.
func (s *TelegramService) IsAllowedUser(userID int64) bool {
	return telegram.IsAllowedUser(s.cfg(), userID)
}

// ProcessIncomingTelegram processes an incoming Telegram message and returns the response body. app must be non-nil.
func (s *TelegramService) ProcessIncomingTelegram(ctx context.Context, app *infra.App, msg *telegram.IncomingMessage) string {
	if msg == nil {
		return ""
	}
	return ProcessIncomingTelegram(ctx, app, msg)
}

// SendMessage sends a message to a Telegram chat via the Bot API.
func (s *TelegramService) SendMessage(ctx context.Context, chatID int64, body string) error {
	return telegram.SendMessage(ctx, s.cfg(), chatID, body, infra.LoggerFrom(ctx))
}
