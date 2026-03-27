package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/telegram"
	"github.com/jackstrohm/jot/pkg/utils"
)

// correlationFields is embedded in task handler request structs to carry Cloud Task tracing ids.
// Call applyToCtx after DecodeAndValidate to propagate them into the context.
type correlationFields struct {
	TaskID        string `json:"task_id"`
	ParentTraceID string `json:"parent_trace_id"`
}

func (c correlationFields) applyToCtx(ctx context.Context) context.Context {
	if c.TaskID != "" || c.ParentTraceID != "" {
		return infra.WithCorrelation(ctx, c.TaskID, c.ParentTraceID)
	}
	return ctx
}

func handleProcessEntry(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	var data struct {
		UUID      string `json:"uuid" validate:"required"`
		Content   string `json:"content" validate:"required"`
		Timestamp string `json:"timestamp"`
		Source    string `json:"source" validate:"required"`
		correlationFields
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		return nil, handlerError(http.StatusBadRequest, err.Error())
	}
	ctx = data.correlationFields.applyToCtx(ctx)
	LogHandlerRequest(ctx, r.Method, path, "uuid", data.UUID, "source", data.Source, "content_length", len(data.Content), "task_id", data.TaskID, "parent_trace_id", data.ParentTraceID)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if _, err := s.Agent.ProcessLogSequential(ctx, data.UUID, data.Content, data.Timestamp, data.Source); err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

// runTelegramMessage processes an incoming Telegram message end-to-end: handles
// images/voice/location, runs the FOH query, and sends the reply. Called from
// handleProcessTelegramQuery (Cloud Tasks path) and the inline goroutine in handleTelegram.
func runTelegramMessage(ctx context.Context, s *Server, msg *telegram.IncomingMessage) error {
	body := msg.Text
	if body == "" && msg.ImageFileID != "" {
		body = "Photo"
	}
	if msg.HasLocation {
		locationStr := reverseGeocode(ctx, msg.Latitude, msg.Longitude)
		body = strings.TrimSpace(body + " " + locationStr)
		infra.LoggerFrom(ctx).Info("telegram: location appended", "chat_id", msg.ChatID, "location", locationStr)
	}

	// Handle image: download, caption, add entry, reply with caption (skip FOH).
	if msg.ImageFileID != "" {
		infra.LoggerFrom(ctx).Info("telegram: processing image", "chat_id", msg.ChatID, "image_file_id", msg.ImageFileID)
		imageBytes, mime, err := s.Telegram.DownloadFileWithMIME(ctx, msg.ImageFileID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("telegram: image download failed, using placeholder", "chat_id", msg.ChatID, "error", err)
		} else {
			infra.LoggerFrom(ctx).Info("telegram: image downloaded", "chat_id", msg.ChatID, "image_bytes", len(imageBytes), "mime", mime)
			userCaption := ""
			if body != "" && body != "Photo" {
				userCaption = body
			}
			caption, capErr := infra.GenerateImageCaption(ctx, s.App.(*infra.App), imageBytes, mime, userCaption, s.Config)
			if capErr != nil {
				infra.LoggerFrom(ctx).Warn("telegram: image caption failed, using body as-is", "chat_id", msg.ChatID, "error", capErr)
			} else {
				body = caption
				infra.LoggerFrom(ctx).Info("telegram: image captioned", "chat_id", msg.ChatID, "caption_len", len(body), "caption_preview", utils.TruncateString(body, 120))
				infra.LoggerFrom(ctx).Debug("telegram: image caption full", "chat_id", msg.ChatID, "caption", body)
			}
			entryUUID, addErr := s.Agent.AddEntry(ctx, body, "telegram", nil, imageBytes)
			if addErr != nil {
				infra.LoggerFrom(ctx).Error("telegram: add image entry failed", "chat_id", msg.ChatID, "error", addErr)
			} else {
				ctx = agent.WithEntryAlreadyAdded(ctx, entryUUID)
			}
		}
		if body == "Photo" {
			if err := s.Telegram.SendMessage(ctx, msg.ChatID, "Photo logged."); err != nil {
				return fmt.Errorf("telegram send reply: %w", err)
			}
			infra.LoggerFrom(ctx).Info("telegram: photo logged reply sent", "chat_id", msg.ChatID)
			return nil
		}
		if _, saveErr := s.App.(*infra.App).Memory.SaveQuery(ctx, "[Photo]", body, "telegram", false); saveErr != nil {
			infra.LoggerFrom(ctx).Warn("telegram: save photo query log failed", "chat_id", msg.ChatID, "error", saveErr)
		}
		reply := body + "\n\nLogged."
		infra.LoggerFrom(ctx).Info("telegram: caption reply sent", "chat_id", msg.ChatID, "preview", utils.TruncateString(reply, 60))
		return sendTelegramResponse(ctx, s, msg.ChatID, reply)
	}

	// Handle voice: download, transcribe, persist, then treat transcript as the query body.
	if msg.VoiceFileID != "" {
		infra.LoggerFrom(ctx).Info("telegram: processing voice note", "chat_id", msg.ChatID, "voice_file_id", msg.VoiceFileID)
		audioBytes, _, err := s.Telegram.DownloadFileWithMIME(ctx, msg.VoiceFileID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("telegram: voice download failed", "chat_id", msg.ChatID, "error", err)
			_ = s.Telegram.SendMessage(ctx, msg.ChatID, "Could not download your voice note. Please try again.")
			return nil
		}
		infra.LoggerFrom(ctx).Info("telegram: voice note downloaded", "chat_id", msg.ChatID, "bytes", len(audioBytes))
		transcript, tErr := infra.TranscribeAudio(ctx, s.App.(*infra.App), audioBytes, s.Config)
		if tErr != nil {
			infra.LoggerFrom(ctx).Warn("telegram: transcription failed", "chat_id", msg.ChatID, "error", tErr)
			_ = s.Telegram.SendMessage(ctx, msg.ChatID, "Could not transcribe your voice note. Please try again.")
			return nil
		}
		infra.LoggerFrom(ctx).Info("telegram: voice transcribed", "chat_id", msg.ChatID, "transcript_len", len(transcript))
		infra.LoggerFrom(ctx).Debug("telegram: voice transcript", "chat_id", msg.ChatID, "transcript", transcript)
		if strings.TrimSpace(transcript) == "" || transcript == "[inaudible]" {
			_ = s.Telegram.SendMessage(ctx, msg.ChatID, "Could not transcribe voice note — audio was silent or unclear.")
			return nil
		}
		app := s.App.(*infra.App)
		audioURL, uploadErr := app.UploadAudio(ctx, audioBytes)
		if uploadErr != nil {
			infra.LoggerFrom(ctx).Warn("telegram: audio GCS upload failed", "chat_id", msg.ChatID, "error", uploadErr)
		}
		entryUUID, addErr := s.Agent.AddEntry(ctx, transcript, "telegram", nil, nil)
		if addErr != nil {
			infra.LoggerFrom(ctx).Error("telegram: add voice entry failed", "chat_id", msg.ChatID, "error", addErr)
		} else {
			ctx = agent.WithEntryAlreadyAdded(ctx, entryUUID)
			if audioURL != "" {
				if updateErr := app.Memory.UpdateEntryAudio(ctx, entryUUID, audioURL, transcript); updateErr != nil {
					infra.LoggerFrom(ctx).Warn("telegram: update audio fields failed", "chat_id", msg.ChatID, "error", updateErr)
				}
			}
		}
		body = transcript
	}

	// Handle slash commands before running FOH.
	if strings.HasPrefix(body, "/") {
		if handled, slashErr := handleTelegramSlashCommand(ctx, s, msg.ChatID, body); handled {
			return slashErr
		}
	}

	// Run FOH and send response.
	fohMsg := &telegram.IncomingMessage{
		UpdateID: msg.UpdateID, MessageID: msg.MessageID,
		ChatID: msg.ChatID, UserID: msg.UserID,
		Text: body, ImageFileID: msg.ImageFileID, VoiceFileID: msg.VoiceFileID,
	}
	response := s.Telegram.ProcessIncomingTelegram(ctx, s.App.(*infra.App), fohMsg)
	if response == "" {
		response = "I couldn't process that. Please try again."
	}
	return sendTelegramResponse(ctx, s, msg.ChatID, response)
}

// handleProcessTelegramQuery runs the full Telegram processing pipeline for a Cloud Tasks dispatch.
// The core logic lives in runTelegramMessage, which is also called directly by the inline goroutine
// in handleTelegram for resilience when Cloud Tasks dispatch is unreliable.
func handleProcessTelegramQuery(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	var data struct {
		ChatID      int64   `json:"chat_id" validate:"required"`
		UserID      int64   `json:"user_id"`
		Body        string  `json:"body"`
		ImageFileID string  `json:"image_file_id"`
		VoiceFileID string  `json:"voice_file_id"`
		UpdateID    int64   `json:"update_id"`
		MessageID   int64   `json:"message_id"`
		HasLocation bool    `json:"has_location"`
		Latitude    float64 `json:"latitude"`
		Longitude   float64 `json:"longitude"`
		correlationFields
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		return nil, handlerError(http.StatusBadRequest, err.Error())
	}
	ctx = data.correlationFields.applyToCtx(ctx)
	msg := &telegram.IncomingMessage{
		UpdateID: data.UpdateID, MessageID: data.MessageID,
		ChatID: data.ChatID, UserID: data.UserID,
		Text: data.Body, ImageFileID: data.ImageFileID, VoiceFileID: data.VoiceFileID,
		HasLocation: data.HasLocation, Latitude: data.Latitude, Longitude: data.Longitude,
	}
	LogHandlerRequest(ctx, r.Method, path, "chat_id", data.ChatID, "update_id", data.UpdateID, "body_length", len(data.Body), "task_id", data.TaskID, "parent_trace_id", data.ParentTraceID)
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if err := runTelegramMessage(ctx, s, msg); err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

// sendTelegramResponse delivers the FOH answer to the user. If the answer contains an
// [SEND_IMAGE:<uuid>] sentinel, the image is fetched from GCS and sent via sendPhoto;
// the remaining text (caption) is sent alongside it. Falls back to sendMessage on error.
func sendTelegramResponse(ctx context.Context, s *Server, chatID int64, response string) error {
	entryUUID, caption, hasImage := utils.ParseImageSentinel(response)
	if !hasImage {
		if err := s.Telegram.SendMessage(ctx, chatID, response); err != nil {
			infra.LoggerFrom(ctx).Error("process-telegram-query: send reply failed", "chat_id", chatID, "error", err)
			return fmt.Errorf("failed to send Telegram reply: %w", err)
		}
		infra.LoggerFrom(ctx).Info("process-telegram-query: reply sent", "chat_id", chatID, "preview", utils.TruncateString(response, 60))
		return nil
	}

	// Fetch the entry's image_url from Firestore.
	app := s.App.(*infra.App)
	entry, err := app.Memory.GetEntry(ctx, entryUUID)
	if err != nil || entry == nil || entry.ImageURL == "" {
		infra.LoggerFrom(ctx).Warn("process-telegram-query: image entry not found, falling back to text", "chat_id", chatID, "entry_uuid", entryUUID)
		return sendFallback(ctx, s, chatID, caption)
	}

	// Download image bytes from GCS.
	imageBytes, mimeType, err := app.ImageStorage().DownloadImage(ctx, entry.ImageURL)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("process-telegram-query: GCS download failed, falling back to text", "chat_id", chatID, "image_url", entry.ImageURL, "error", err)
		return sendFallback(ctx, s, chatID, caption)
	}

	// Send image via sendPhoto.
	if err := s.Telegram.SendPhoto(ctx, chatID, caption, imageBytes, mimeType); err != nil {
		infra.LoggerFrom(ctx).Warn("process-telegram-query: sendPhoto failed, falling back to text", "chat_id", chatID, "error", err)
		return sendFallback(ctx, s, chatID, caption)
	}
	infra.LoggerFrom(ctx).Info("process-telegram-query: photo sent", "chat_id", chatID, "entry_uuid", entryUUID, "bytes", len(imageBytes))
	return nil
}

// sendFallback sends the caption as a plain text message (used when image delivery fails).
func sendFallback(ctx context.Context, s *Server, chatID int64, caption string) error {
	if caption == "" {
		caption = "I found the image but couldn't send it at this time."
	}
	if err := s.Telegram.SendMessage(ctx, chatID, caption); err != nil {
		infra.LoggerFrom(ctx).Error("process-telegram-query: fallback send failed", "chat_id", chatID, "error", err)
		return fmt.Errorf("failed to send Telegram reply: %w", err)
	}
	return nil
}

func handleSaveQuery(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	var data struct {
		Question string `json:"question" validate:"required"`
		Answer   string `json:"answer"`
		Source   string `json:"source" validate:"required"`
		IsGap    bool   `json:"is_gap"`
		correlationFields
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		return nil, handlerError(http.StatusBadRequest, err.Error())
	}
	ctx = data.correlationFields.applyToCtx(ctx)
	LogHandlerRequest(ctx, r.Method, path, "question_preview", utils.TruncateString(data.Question, 60), "source", data.Source, "is_gap", data.IsGap, "task_id", data.TaskID, "parent_trace_id", data.ParentTraceID)
	if _, err := s.Journal.SaveQuery(ctx, data.Question, data.Answer, data.Source, data.IsGap); err != nil {
		infra.LoggerFrom(ctx).Error("save-query failed", "error", err)
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

func handleBackfillEmbeddings(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	LogHandlerRequest(ctx, r.Method, path, "limit", limit)
	processed, err := s.Journal.BackfillEntryEmbeddings(ctx, limit)
	if err != nil {
		infra.LoggerFrom(ctx).Error("backfill-embeddings failed", "error", err)
		return nil, err
	}
	infra.LoggerFrom(ctx).Info("backfill-embeddings completed", "processed", processed)
	return map[string]interface{}{"success": true, "processed": processed}, nil
}

// reverseGeocode calls BigDataCloud's free reverse-geocoding API and returns a formatted location
// string like "[Location: 37.77, -122.41 (San Francisco, CA)]". On any failure it falls back to
// "[Location: {lat}, {lng}]" so the journal entry is always saved with at least the raw coords.
func reverseGeocode(ctx context.Context, lat, lng float64) string {
	fallback := fmt.Sprintf("[Location: %.4f, %.4f]", lat, lng)

	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://api.bigdatacloud.net/data/reverse-geocode-client?latitude=%f&longitude=%f&localityLanguage=en", lat, lng)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return fallback
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallback
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fallback
	}

	var result struct {
		City                   string `json:"city"`
		Locality               string `json:"locality"`
		PrincipalSubdivision   string `json:"principalSubdivision"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fallback
	}

	place := result.City
	if place == "" {
		place = result.Locality
	}
	if place == "" && result.PrincipalSubdivision == "" {
		return fallback
	}

	if place != "" && result.PrincipalSubdivision != "" {
		return fmt.Sprintf("[Location: %.4f, %.4f (%s, %s)]", lat, lng, place, result.PrincipalSubdivision)
	}
	if place != "" {
		return fmt.Sprintf("[Location: %.4f, %.4f (%s)]", lat, lng, place)
	}
	return fmt.Sprintf("[Location: %.4f, %.4f (%s)]", lat, lng, result.PrincipalSubdivision)
}
