package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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
	telegramAPIBase  = "https://api.telegram.org/bot"
	telegramFileBase = "https://api.telegram.org/file/bot"
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

// TgLocation represents a geographic location sent in a Telegram message.
type TgLocation struct {
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

// TgMsg is the message part of an Update (chat, from, text/caption, photo).
type TgMsg struct {
	MessageID int64         `json:"message_id"`
	Chat      TgChat        `json:"chat"`
	From      *TgUser       `json:"from"`
	Text      string        `json:"text"`
	Caption   string        `json:"caption"`
	Photo     []TgPhotoSize `json:"photo"`
	Location  *TgLocation   `json:"location"`
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
// When the message is a location pin, HasLocation is true and Latitude/Longitude are populated.
type IncomingMessage struct {
	UpdateID    int64
	MessageID   int64
	ChatID      int64
	UserID      int64
	Text        string
	ImageFileID string  // set when message has photo; use DownloadFile to get bytes
	HasLocation bool    // set when message is a location pin
	Latitude    float64 // set when HasLocation is true
	Longitude   float64 // set when HasLocation is true
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

	imageFileID := ""
	if len(msg.Photo) > 0 {
		// Telegram sends same photo in multiple sizes; last element is largest.
		imageFileID = msg.Photo[len(msg.Photo)-1].FileID
		if text == "" {
			text = "Photo"
		}
	}

	hasLocation := false
	var lat, lng float64
	if msg.Location != nil {
		hasLocation = true
		lat = msg.Location.Latitude
		lng = msg.Location.Longitude
		if text == "" && imageFileID == "" {
			text = "Shared location pin"
		}
	}

	userID := int64(0)
	if msg.From != nil {
		userID = msg.From.ID
	}

	return &u, &IncomingMessage{
		UpdateID:    u.UpdateID,
		MessageID:   msg.MessageID,
		ChatID:      msg.Chat.ID,
		UserID:      userID,
		Text:        text,
		ImageFileID: imageFileID,
		HasLocation: hasLocation,
		Latitude:    lat,
		Longitude:   lng,
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

// MaxDownloadBytes is the maximum size we will download for a file (e.g. photo) from Telegram.
const MaxDownloadBytes = 10 * 1024 * 1024 // 10 MiB

// getFileResponse is the JSON shape of Telegram getFile API result. FileSize may be omitted by the API.
type getFileResponse struct {
	OK     bool `json:"ok"`
	Result struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size,omitempty"`
	} `json:"result"`
}

// DownloadFile downloads the file identified by fileID from Telegram (e.g. photo from message.photo).
// Returns the file bytes and a MIME type (detected from magic bytes or default image/jpeg).
// Respects ctx cancellation and limits size to MaxDownloadBytes.
func DownloadFile(ctx context.Context, cfg *config.Config, fileID string, log Logger) ([]byte, string, error) {
	if cfg == nil || cfg.TelegramBotToken == "" {
		return nil, "", fmt.Errorf("telegram bot token not configured")
	}
	// 1. getFile to obtain file_path (POST form or GET with file_id)
	getURL := telegramAPIBase + cfg.TelegramBotToken + "/getFile"
	form := url.Values{}
	form.Set("file_id", fileID)
	req, err := http.NewRequestWithContext(ctx, "POST", getURL, bytes.NewReader([]byte(form.Encode())))
	if err != nil {
		return nil, "", fmt.Errorf("create getFile request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("getFile request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, "", fmt.Errorf("read getFile response: %w", err)
	}
	var fileResp getFileResponse
	if err := json.Unmarshal(respBody, &fileResp); err != nil {
		return nil, "", fmt.Errorf("parse getFile response: %w", err)
	}
	if !fileResp.OK || fileResp.Result.FilePath == "" {
		return nil, "", fmt.Errorf("getFile failed or missing file_path: %s", string(respBody))
	}
	filePath := fileResp.Result.FilePath
	if fileResp.Result.FileSize > 0 && fileResp.Result.FileSize > MaxDownloadBytes {
		return nil, "", fmt.Errorf("file too large: %d bytes (max %d)", fileResp.Result.FileSize, MaxDownloadBytes)
	}
	// 2. Download file from https://api.telegram.org/file/bot<token>/<file_path>
	downloadURL := telegramFileBase + cfg.TelegramBotToken + "/" + filePath
	dlReq, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create download request: %w", err)
	}
	dlResp, err := client.Do(dlReq)
	if err != nil {
		return nil, "", fmt.Errorf("download file: %w", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(dlResp.Body, 512))
		return nil, "", fmt.Errorf("download failed: status=%d body=%s", dlResp.StatusCode, string(b))
	}
	data, err := io.ReadAll(io.LimitReader(dlResp.Body, MaxDownloadBytes))
	if err != nil {
		return nil, "", fmt.Errorf("read file: %w", err)
	}
	mime := detectImageMIME(data)
	if log != nil {
		log.Info("Telegram file downloaded", "file_id", fileID, "bytes", len(data), "mime", mime)
	}
	return data, mime, nil
}

// detectImageMIME returns a MIME type from magic bytes (JPEG, PNG, WebP, GIF); default image/jpeg.
func detectImageMIME(data []byte) string {
	if len(data) < 12 {
		return "image/jpeg"
	}
	if data[0] == 0xFF && data[1] == 0xD8 {
		return "image/jpeg"
	}
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png"
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	if data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 {
		return "image/gif"
	}
	return "image/jpeg"
}

// SendPhoto sends a photo to a Telegram chat via the Bot API using multipart upload.
// caption is optional (max 1024 bytes). imageBytes must be non-empty.
func SendPhoto(ctx context.Context, cfg *config.Config, chatID int64, caption string, imageBytes []byte, mimeType string, log Logger) error {
	if cfg == nil || cfg.TelegramBotToken == "" {
		return fmt.Errorf("telegram bot token not configured")
	}
	if len(imageBytes) == 0 {
		return fmt.Errorf("image bytes are empty")
	}

	apiURL := telegramAPIBase + cfg.TelegramBotToken + "/sendPhoto"

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	if err := mw.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return fmt.Errorf("write chat_id field: %w", err)
	}
	if caption != "" {
		if err := mw.WriteField("caption", truncateToMaxBytes(caption, 1024)); err != nil {
			return fmt.Errorf("write caption field: %w", err)
		}
	}

	filename := "photo.jpg"
	switch mimeType {
	case "image/png":
		filename = "photo.png"
	case "image/webp":
		filename = "photo.webp"
	case "image/gif":
		filename = "photo.gif"
	}
	fw, err := mw.CreateFormFile("photo", filename)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, bytes.NewReader(imageBytes)); err != nil {
		return fmt.Errorf("write photo bytes: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, &buf)
	if err != nil {
		return fmt.Errorf("create sendPhoto request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send photo: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram sendPhoto API error: status=%d body=%s", resp.StatusCode, string(respBody))
	}
	if log != nil {
		log.Info("Telegram photo sent", "chat_id", chatID, "bytes", len(imageBytes))
	}
	return nil
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
