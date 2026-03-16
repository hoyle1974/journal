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

const (
	telegramAPIBase   = "https://api.telegram.org/bot"
	telegramFileBase  = "https://api.telegram.org/file/bot"
)

// WebhookUpdate represents an incoming Telegram webhook payload (subset of Update we care about).
type WebhookUpdate struct {
	UpdateID int64   `json:"update_id"`
	Message  *TgMsg  `json:"message"`
	Edited   *TgMsg  `json:"edited_message"`
}

// TgPhotoSize is one size of a photo (Telegram sends multiple; use the last for highest resolution).
type TgPhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int    `json:"file_size"`
}

// TgMsg is the message part of an Update (chat, from, text/caption, photo).
type TgMsg struct {
	MessageID int64         `json:"message_id"`
	Chat      TgChat        `json:"chat"`
	From      *TgUser       `json:"from"`
	Text      string        `json:"text"`
	Caption   string        `json:"caption"`
	Photo     []TgPhotoSize `json:"photo"`
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
// When the message is a photo, Text is the caption and ImageFileID is the file_id of the largest photo.
type IncomingMessage struct {
	UpdateID    int64
	MessageID   int64
	ChatID      int64
	UserID      int64
	Text        string
	ImageFileID string // set when message has photo; use DownloadFile to get bytes
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

	imageFileID := ""
	if len(msg.Photo) > 0 {
		imageFileID = msg.Photo[len(msg.Photo)-1].FileID
	}

	return &u, &IncomingMessage{
		UpdateID:    u.UpdateID,
		MessageID:   msg.MessageID,
		ChatID:      msg.Chat.ID,
		UserID:      userID,
		Text:        text,
		ImageFileID: imageFileID,
	}, nil
}

// getFileResponse is the JSON response from Telegram getFile API.
type getFileResponse struct {
	OK     bool `json:"ok"`
	Result struct {
		FilePath string `json:"file_path"`
	} `json:"result"`
}

// DownloadFile fetches file bytes from Telegram by file_id. Uses getFile then downloads from the returned file_path.
func DownloadFile(ctx context.Context, cfg *config.Config, fileID string) ([]byte, error) {
	if cfg == nil || cfg.TelegramBotToken == "" {
		return nil, fmt.Errorf("telegram bot token not configured")
	}
	if fileID == "" {
		return nil, fmt.Errorf("file_id is required")
	}
	apiURL := telegramAPIBase + cfg.TelegramBotToken + "/getFile?file_id=" + url.QueryEscape(fileID)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create getFile request: %w", err)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getFile: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out getFileResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse getFile response: %w", err)
	}
	if !out.OK || out.Result.FilePath == "" {
		return nil, fmt.Errorf("getFile failed or missing file_path")
	}
	downloadURL := telegramFileBase + cfg.TelegramBotToken + "/" + out.Result.FilePath
	req2, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	resp2, err := client.Do(req2)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download file: status %d", resp2.StatusCode)
	}
	return io.ReadAll(resp2.Body)
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
