package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/jackstrohm/jot/internal/config"
)

// Logger is used for validation and send logging. Callers pass e.g. infra.LoggerFrom(ctx).
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

const telegramAPIBase = "https://api.telegram.org/bot"

// WebhookUpdate represents an incoming Telegram webhook payload (subset of Update we care about).
type WebhookUpdate struct {
	UpdateID int64   `json:"update_id"`
	Message  *TgMsg  `json:"message"`
	Edited   *TgMsg  `json:"edited_message"`
}

// TgMsg is the message part of an Update (chat, from, text/caption).
type TgMsg struct {
	MessageID int64    `json:"message_id"`
	Chat      TgChat   `json:"chat"`
	From      *TgUser  `json:"from"`
	Text      string   `json:"text"`
	Caption   string   `json:"caption"`
}

// TgChat has the chat id (required for replying).
type TgChat struct {
	ID int64 `json:"id"`
}

// TgUser has the user id (for allowlist).
type TgUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// IncomingMessage normalizes an incoming webhook to a single message + chat + sender for processing.
type IncomingMessage struct {
	UpdateID  int64
	MessageID int64
	ChatID    int64
	UserID    int64
	Text      string
}

// ValidateSecretToken checks the X-Telegram-Bot-Api-Secret-Token header when secret is configured.
func ValidateSecretToken(cfg *config.Config, r *http.Request, log Logger) bool {
	if cfg == nil || cfg.TelegramSecretToken == "" {
		return true
	}
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if got == "" {
		if log != nil {
			log.Warn("missing X-Telegram-Bot-Api-Secret-Token header")
		}
		return false
	}
	if got != cfg.TelegramSecretToken {
		if log != nil {
			log.Warn("invalid Telegram secret token")
		}
		return false
	}
	return true
}

// ParseWebhook parses the request body as a Telegram Update and returns the first text message, if any.
func ParseWebhook(r *http.Request) (*WebhookUpdate, *IncomingMessage, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	var u WebhookUpdate
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, nil, fmt.Errorf("parse webhook json: %w", err)
	}

	msg := u.Message
	if msg == nil {
		msg = u.Edited
	}
	if msg == nil {
		return &u, nil, nil
	}

	text := msg.Text
	if text == "" && msg.Caption != "" {
		text = msg.Caption
	}

	userID := int64(0)
	if msg.From != nil {
		userID = msg.From.ID
	}

	return &u, &IncomingMessage{
		UpdateID:  u.UpdateID,
		MessageID: msg.MessageID,
		ChatID:    msg.Chat.ID,
		UserID:    userID,
		Text:      text,
	}, nil
}

// IsAllowedUser returns true if no allowlist is configured or the user id is in the allowlist.
func IsAllowedUser(cfg *config.Config, userID int64) bool {
	if cfg == nil || cfg.AllowedTelegramUserID == "" {
		return true
	}
	allowed, err := strconv.ParseInt(cfg.AllowedTelegramUserID, 10, 64)
	if err != nil {
		return false
	}
	return userID == allowed
}

func truncateToMaxBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	runes := []rune(s)
	n := 0
	for i, r := range runes {
		n += utf8.RuneLen(r)
		if n > maxBytes {
			if i == 0 {
				return ""
			}
			return string(runes[:i])
		}
	}
	return s
}

// SendMessage sends a text message to a Telegram chat via the Bot API.
func SendMessage(ctx context.Context, cfg *config.Config, chatID int64, text string, log Logger) error {
	if cfg == nil || cfg.TelegramBotToken == "" {
		return fmt.Errorf("telegram bot token not configured")
	}

	// Telegram message limit is 4096 UTF-8 characters.
	if len(text) > 4090 {
		text = truncateToMaxBytes(text, 4087) + "..."
	}

	apiURL := telegramAPIBase + cfg.TelegramBotToken + "/sendMessage"
	data := url.Values{}
	data.Set("chat_id", strconv.FormatInt(chatID, 10))
	data.Set("text", text)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader([]byte(data.Encode())))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram API error: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	if log != nil {
		log.Info("Telegram message sent", "chat_id", chatID, "length", len(text))
	}
	return nil
}
