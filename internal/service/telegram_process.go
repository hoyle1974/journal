package service

import (
	"context"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/telegram"
)

// ProcessIncomingTelegram processes an incoming Telegram message and returns the response. app must be non-nil.
func ProcessIncomingTelegram(ctx context.Context, app *infra.App, msg *telegram.IncomingMessage) string {
	ctx, span := infra.StartSpan(ctx, "telegram.process_message")
	defer span.End()

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return "Empty message received."
	}

	infra.LoggerFrom(ctx).Info("processing incoming Telegram",
		"chat_id", msg.ChatID,
		"user_id", msg.UserID,
		"update_id", msg.UpdateID,
		"body_length", len(text),
	)

	return processQueryTelegram(ctx, app, text, msg.ChatID)
}

func processQueryTelegram(ctx context.Context, app *infra.App, query string, chatID int64) string {
	ctx, span := infra.StartSpan(ctx, "telegram.process_query")
	defer span.End()

	if app == nil {
		return "Service unavailable. Please try again later."
	}
	result := RunQuery(ctx, app, query, "telegram")
	if result.Error {
		infra.LoggerFrom(ctx).Error("telegram query failed", "answer", result.Answer)
		return "Sorry, I couldn't process your query. Please try again."
	}
	return result.Answer
}
