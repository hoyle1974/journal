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
	"github.com/jackstrohm/jot/internal/gdoc"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/sms"
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
	breakdown, entryReport, err := s.Agent.ProcessEntry(ctx, data.UUID, data.Content, data.Timestamp, data.Source)
	if setter, ok := w.(interface{ SetLatencyBreakdown(*infra.LatencyBreakdown) }); ok && breakdown != nil {
		setter.SetLatencyBreakdown(breakdown)
	}
	if err != nil {
		return nil, err
	}
	app, hasApp := s.App.(*infra.App)
	if s.Config != nil && s.Config.DebugReportEnabled && hasApp && entryReport != nil {
		asyncCtx := context.WithoutCancel(ctx)
		cfg := s.Config
		report := entryReport
		app.SubmitAsync(func() {
			narrative := agent.GenerateProcessEntryReport(asyncCtx, app, report)
			if narrative != "" {
				gdoc.WriteReport(asyncCtx, cfg, narrative)
			}
		})
	}
	return map[string]string{"status": "ok"}, nil
}

// handleProcessSMSQuery runs the query for an incoming SMS (FOH) and sends the reply via Twilio.
// Invoked by a Cloud Task enqueued from the SMS webhook so the work runs in a request-scoped context (Cloud Run keeps the request alive until done).
func handleProcessSMSQuery(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	var data struct {
		From       string `json:"from" validate:"required"`
		Body       string `json:"body" validate:"required"`
		MessageSid string `json:"message_sid"`
		correlationFields
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		return nil, handlerError(http.StatusBadRequest, err.Error())
	}
	ctx = data.correlationFields.applyToCtx(ctx)
	msg := &sms.TwilioWebhookRequest{
		MessageSid: data.MessageSid,
		From:       data.From,
		To:         "",
		Body:       data.Body,
	}
	LogHandlerRequest(ctx, r.Method, path, "from", data.From, "message_sid", data.MessageSid, "body_length", len(data.Body), "task_id", data.TaskID, "parent_trace_id", data.ParentTraceID)
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	response := s.SMS.ProcessIncomingSMS(ctx, s.App.(*infra.App), msg)
	if response == "" {
		response = "I couldn't process that. Please try again."
	}
	if err := s.SMS.SendSMS(ctx, data.From, response); err != nil {
		infra.LoggerFrom(ctx).Error("process-sms-query: send reply failed", "to", data.From, "error", err)
		return nil, fmt.Errorf("failed to send SMS reply: %w", err)
	}
	infra.LoggerFrom(ctx).Info("process-sms-query: reply sent", "to", data.From, "preview", utils.TruncateString(response, 60))
	return map[string]string{"status": "ok"}, nil
}

// handleProcessTelegramQuery runs the query for an incoming Telegram message (FOH) and sends the reply via Telegram.
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
	if data.Body == "" && data.ImageFileID == "" && data.VoiceFileID == "" && !data.HasLocation {
		infra.LoggerFrom(ctx).Info("process-telegram-query: empty body, no image, no voice, and no location, sending hint", "chat_id", data.ChatID)
		_ = s.Telegram.SendMessage(ctx, data.ChatID, "I didn't receive any text, image, or voice note. Send a message, photo, or voice note to log something.")
		return map[string]string{"status": "ok"}, nil
	}
	if data.Body == "" && data.ImageFileID != "" {
		data.Body = "Photo"
	}
	if data.HasLocation {
		locationStr := reverseGeocode(ctx, data.Latitude, data.Longitude)
		data.Body = strings.TrimSpace(data.Body + " " + locationStr)
		infra.LoggerFrom(ctx).Info("process-telegram-query: location appended", "chat_id", data.ChatID, "location", locationStr)
	}
	ctx = data.correlationFields.applyToCtx(ctx)
	// When message has an image, download it, optionally generate a caption, then create a journal entry (upload to GCS) and pass entry UUID so FOH skips adding a duplicate.
	var imageBytes []byte
	if data.ImageFileID != "" {
		infra.LoggerFrom(ctx).Info("process-telegram-query: processing image, downloading", "chat_id", data.ChatID, "image_file_id", data.ImageFileID)
		var mime string
		var err error
		imageBytes, mime, err = s.Telegram.DownloadFileWithMIME(ctx, data.ImageFileID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("process-telegram-query: download image failed, using placeholder", "chat_id", data.ChatID, "error", err)
		} else {
			infra.LoggerFrom(ctx).Info("process-telegram-query: image downloaded",
				"chat_id", data.ChatID,
				"image_bytes", len(imageBytes),
				"mime", mime,
				"telegram_file_id", data.ImageFileID,
			)
			userCaption := ""
			if data.Body != "" && data.Body != "Photo" {
				userCaption = data.Body
			}
			caption, err := infra.GenerateImageCaption(ctx, s.App.(*infra.App), imageBytes, mime, userCaption, s.Config)
			if err != nil {
				infra.LoggerFrom(ctx).Warn("process-telegram-query: image caption failed, using body as-is", "chat_id", data.ChatID, "error", err)
			} else {
				data.Body = caption
				infra.LoggerFrom(ctx).Info("process-telegram-query: image caption generated",
					"chat_id", data.ChatID,
					"caption_len", len(data.Body),
					"caption_preview", utils.TruncateString(data.Body, 120),
				)
				infra.LoggerFrom(ctx).Debug("process-telegram-query: image caption full", "chat_id", data.ChatID, "caption", data.Body)
			}
		}
		entryUUID, err := s.Agent.AddEntry(ctx, data.Body, "telegram", nil, imageBytes)
		if err != nil {
			infra.LoggerFrom(ctx).Error("process-telegram-query: add entry for image failed", "chat_id", data.ChatID, "error", err)
		} else {
			ctx = agent.WithEntryAlreadyAdded(ctx, entryUUID)
		}
		// Image with no meaningful caption: confirm log only.
		if data.Body == "Photo" {
			response := "Photo logged."
			if err := s.Telegram.SendMessage(ctx, data.ChatID, response); err != nil {
				infra.LoggerFrom(ctx).Error("process-telegram-query: send reply failed", "chat_id", data.ChatID, "error", err)
				return nil, fmt.Errorf("failed to send Telegram reply: %w", err)
			}
			infra.LoggerFrom(ctx).Info("process-telegram-query: reply sent", "chat_id", data.ChatID, "preview", response)
			return map[string]string{"status": "ok"}, nil
		}
		// Image with generated caption: return the caption to the user and confirm log (skip FOH).
		// Save a query log so the image event appears in the recent conversation context for future queries.
		if _, saveErr := s.App.(*infra.App).Memory.SaveQuery(ctx, "[Photo]", data.Body, "telegram", false); saveErr != nil {
			infra.LoggerFrom(ctx).Warn("process-telegram-query: save query log for image failed", "chat_id", data.ChatID, "error", saveErr)
		}
		response := data.Body + "\n\nLogged."
		if err := s.Telegram.SendMessage(ctx, data.ChatID, response); err != nil {
			infra.LoggerFrom(ctx).Error("process-telegram-query: send reply failed", "chat_id", data.ChatID, "error", err)
			return nil, fmt.Errorf("failed to send Telegram reply: %w", err)
		}
		infra.LoggerFrom(ctx).Info("process-telegram-query: reply sent (caption)", "chat_id", data.ChatID, "preview", utils.TruncateString(response, 60))
		return map[string]string{"status": "ok"}, nil
	}
	// Handle voice note: download, transcribe via Gemini, persist audio+transcription, then run FOH
	// with the transcription as if the user had typed it.
	if data.VoiceFileID != "" {
		infra.LoggerFrom(ctx).Info("process-telegram-query: processing voice note", "chat_id", data.ChatID, "voice_file_id", data.VoiceFileID)
		audioBytes, _, err := s.Telegram.DownloadFileWithMIME(ctx, data.VoiceFileID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("process-telegram-query: voice download failed", "chat_id", data.ChatID, "error", err)
			_ = s.Telegram.SendMessage(ctx, data.ChatID, "Could not download your voice note. Please try again.")
			return map[string]string{"status": "ok"}, nil
		}
		infra.LoggerFrom(ctx).Info("process-telegram-query: voice note downloaded", "chat_id", data.ChatID, "bytes", len(audioBytes))

		transcript, err := infra.TranscribeAudio(ctx, s.App.(*infra.App), audioBytes, s.Config)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("process-telegram-query: transcription failed", "chat_id", data.ChatID, "error", err)
			_ = s.Telegram.SendMessage(ctx, data.ChatID, "Could not transcribe your voice note. Please try again.")
			return map[string]string{"status": "ok"}, nil
		}
		infra.LoggerFrom(ctx).Info("process-telegram-query: transcribed", "chat_id", data.ChatID, "transcript_len", len(transcript))
		infra.LoggerFrom(ctx).Debug("process-telegram-query: transcript", "chat_id", data.ChatID, "transcript", transcript)

		if strings.TrimSpace(transcript) == "" || transcript == "[inaudible]" {
			_ = s.Telegram.SendMessage(ctx, data.ChatID, "Could not transcribe voice note — audio was silent or unclear.")
			return map[string]string{"status": "ok"}, nil
		}

		// Upload audio to GCS and create a journal entry with the transcription.
		app := s.App.(*infra.App)
		audioURL, uploadErr := app.UploadAudio(ctx, audioBytes)
		if uploadErr != nil {
			infra.LoggerFrom(ctx).Warn("process-telegram-query: audio GCS upload failed", "chat_id", data.ChatID, "error", uploadErr)
		}
		entryUUID, addErr := s.Agent.AddEntry(ctx, transcript, "telegram", nil, nil)
		if addErr != nil {
			infra.LoggerFrom(ctx).Error("process-telegram-query: add entry for voice failed", "chat_id", data.ChatID, "error", addErr)
		} else {
			ctx = agent.WithEntryAlreadyAdded(ctx, entryUUID)
			if audioURL != "" {
				if updateErr := app.Memory.UpdateEntryAudio(ctx, entryUUID, audioURL, transcript); updateErr != nil {
					infra.LoggerFrom(ctx).Warn("process-telegram-query: update entry audio fields failed", "chat_id", data.ChatID, "error", updateErr)
				}
			}
		}

		// Use the transcription as the query body — treat it as if the user typed it.
		data.Body = transcript
	}

	// Handle slash commands before running FOH.
	if strings.HasPrefix(data.Body, "/") {
		if handled, slashErr := handleTelegramSlashCommand(ctx, s, data.ChatID, data.Body); handled {
			return map[string]string{"status": "ok"}, slashErr
		}
	}

	msg := &telegram.IncomingMessage{
		UpdateID:    data.UpdateID,
		MessageID:   data.MessageID,
		ChatID:      data.ChatID,
		UserID:      data.UserID,
		Text:        data.Body,
		ImageFileID: data.ImageFileID,
		VoiceFileID: data.VoiceFileID,
	}
	LogHandlerRequest(ctx, r.Method, path, "chat_id", data.ChatID, "update_id", data.UpdateID, "body_length", len(data.Body), "task_id", data.TaskID, "parent_trace_id", data.ParentTraceID)
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	response := s.Telegram.ProcessIncomingTelegram(ctx, s.App.(*infra.App), msg)
	if response == "" {
		response = "I couldn't process that. Please try again."
	}
	if err := sendTelegramResponse(ctx, s, data.ChatID, response); err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

// sendTelegramResponse delivers the FOH answer to the user. If the answer contains an
// [SEND_IMAGE:<uuid>] sentinel, the image is fetched from GCS and sent via sendPhoto;
// the remaining text (caption) is sent alongside it. Falls back to sendMessage on error.
func sendTelegramResponse(ctx context.Context, s *Server, chatID int64, response string) error {
	entryUUID, caption, hasImage := parseSentinel(response)
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

// parseSentinel extracts an image sentinel [SEND_IMAGE:<uuid>] from a response string.
// Returns the entry UUID, the remaining caption text (sentinel stripped), and whether a sentinel was found.
func parseSentinel(response string) (entryUUID, caption string, ok bool) {
	const prefix = "[SEND_IMAGE:"
	start := strings.Index(response, prefix)
	if start < 0 {
		return "", "", false
	}
	end := strings.Index(response[start:], "]")
	if end < 0 {
		return "", "", false
	}
	end += start
	entryUUID = strings.TrimSpace(response[start+len(prefix) : end])
	if entryUUID == "" {
		return "", "", false
	}
	caption = strings.TrimSpace(response[:start] + response[end+1:])
	return entryUUID, caption, true
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
		infra.ErrorsTotal.Inc()
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
