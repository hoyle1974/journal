package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

func handleTelegram(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	ctx, span := infra.StartSpan(ctx, "telegram.webhook")
	defer span.End()
	infra.LoggerFrom(ctx).Debug("telegram webhook received", "method", r.Method, "content_length", r.ContentLength)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	if !s.Telegram.ValidateSecretToken(r) {
		infra.LoggerFrom(ctx).Warn("invalid Telegram secret token")
		LogHandlerResponse(ctx, r.Method, path, http.StatusUnauthorized, "error", "Invalid secret token")
		WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid secret token"})
		return
	}
	rawUpdate, incoming, err := s.Telegram.ParseWebhook(r)
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to parse Telegram webhook", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "Invalid request")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}
	if incoming == nil {
		updateID := int64(0)
		if rawUpdate != nil {
			updateID = rawUpdate.UpdateID
		}
		infra.LoggerFrom(ctx).Debug("telegram webhook: update has no message", "update_id", updateID)
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ignored", "reason", "no message")
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "true"})
		return
	}
	infra.LoggerFrom(ctx).Debug("telegram webhook parsed",
		"update_id", incoming.UpdateID,
		"message_id", incoming.MessageID,
		"chat_id", incoming.ChatID,
		"user_id", incoming.UserID,
		"body", incoming.Text,
		"body_len", len(incoming.Text),
		"image_file_id", incoming.ImageFileID,
		"voice_file_id", incoming.VoiceFileID,
	)
	span.SetAttributes(map[string]string{"telegram.chat_id": fmt.Sprintf("%d", incoming.ChatID), "telegram.update_id": fmt.Sprintf("%d", incoming.UpdateID)})
	bodyPreview := incoming.Text
	if bodyPreview == "" {
		bodyPreview = "(empty)"
	} else {
		bodyPreview = utils.TruncateString(bodyPreview, 80)
	}
	LogHandlerRequest(ctx, r.Method, path, "chat_id", incoming.ChatID, "user_id", incoming.UserID, "update_id", incoming.UpdateID, "body_preview", bodyPreview)
	if !s.Telegram.IsAllowedUser(incoming.UserID) {
		infra.LoggerFrom(ctx).Debug("telegram webhook: ignoring unauthorized user", "user_id", incoming.UserID, "chat_id", incoming.ChatID, "update_id", incoming.UpdateID)
		infra.LoggerFrom(ctx).Warn("Telegram from unauthorized user", "user_id", incoming.UserID)
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ignored", "reason", "unauthorized user")
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "true"})
		return
	}
	if incoming.Text == "" && incoming.ImageFileID == "" && incoming.VoiceFileID == "" && !incoming.HasLocation {
		infra.LoggerFrom(ctx).Debug("telegram webhook: no text, image, voice, or location, sending hint", "chat_id", incoming.ChatID, "update_id", incoming.UpdateID)
		infra.LoggerFrom(ctx).Info("telegram message has no text, image, voice, or location, sending hint", "chat_id", incoming.ChatID)
		_ = s.Telegram.SendMessage(ctx, incoming.ChatID, "Send a text message, photo, or voice note to log something.")
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ignored", "reason", "no text, image, or voice")
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "true"})
		return
	}
	infra.LoggerFrom(ctx).Debug("telegram webhook: accepted, enqueueing task", "chat_id", incoming.ChatID, "update_id", incoming.UpdateID, "has_image", incoming.ImageFileID != "", "has_voice", incoming.VoiceFileID != "")
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "accepted", "chat_id", incoming.ChatID)
	WriteJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	infra.LoggerFrom(ctx).Info("telegram responded 200, processing in background")

	// When message includes a photo, add a journal entry with image before enqueueing the query task.
	if incoming.ImageFileID != "" {
		imageBytes, err := s.Telegram.DownloadFile(ctx, incoming.ImageFileID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("telegram photo download failed", "error", err)
		} else {
			content := incoming.Text
			if content == "" {
				content = "Photo"
			}
			if _, addErr := s.Agent.AddEntry(ctx, content, "telegram", nil, imageBytes); addErr != nil {
				infra.LoggerFrom(ctx).Warn("telegram add entry with image failed", "error", addErr)
			}
		}
	}

	// When message is a voice note, download and store audio in GCS before enqueueing.
	// The task handler will transcribe and run FOH; we store early so the audio is persisted.
	if incoming.VoiceFileID != "" {
		audioBytes, _, err := s.Telegram.DownloadFileWithMIME(ctx, incoming.VoiceFileID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("telegram voice note download failed", "error", err)
		} else {
			app := s.App.(*infra.App)
			if _, uploadErr := app.UploadAudio(ctx, audioBytes); uploadErr != nil {
				infra.LoggerFrom(ctx).Warn("telegram voice note GCS upload failed", "error", uploadErr)
			}
		}
	}

	taskID := "process-telegram-query-" + infra.GenShortRunID()
	parentTraceID := infra.TraceIDFromContext(ctx)
	payload := map[string]interface{}{
		"chat_id":         incoming.ChatID,
		"user_id":         incoming.UserID,
		"body":            incoming.Text,
		"update_id":       incoming.UpdateID,
		"message_id":      incoming.MessageID,
		"task_id":         taskID,
		"parent_trace_id": parentTraceID,
	}
	if incoming.ImageFileID != "" {
		payload["image_file_id"] = incoming.ImageFileID
	}
	if incoming.VoiceFileID != "" {
		payload["voice_file_id"] = incoming.VoiceFileID
	}
	if incoming.HasLocation {
		payload["has_location"] = true
		payload["latitude"] = incoming.Latitude
		payload["longitude"] = incoming.Longitude
	}
	infra.LoggerFrom(ctx).Debug("telegram webhook: enqueueing task", "task_id", taskID, "parent_trace_id", parentTraceID, "body_len", len(incoming.Text), "has_image", incoming.ImageFileID != "", "has_voice", incoming.VoiceFileID != "")
	enqErr := s.App.EnqueueTask(ctx, "/internal/process-telegram-query", payload)
	if enqErr == nil {
		infra.LoggerFrom(ctx).Debug("telegram enqueued for process-telegram-query", "chat_id", incoming.ChatID, "task_id", taskID, "parent_trace_id", parentTraceID)
		return
	}
	infra.LoggerFrom(ctx).Debug("telegram webhook: enqueue failed, falling back to goroutine", "chat_id", incoming.ChatID, "task_id", taskID, "error", enqErr)
	infra.LoggerFrom(ctx).Warn("telegram task enqueue failed, processing in goroutine", "chat_id", incoming.ChatID, "error", enqErr)
	go func() {
		bgCtx := s.App.WithContext(context.Background())
		bgCtx = infra.WithCorrelation(bgCtx, taskID, parentTraceID)
		infra.LoggerFrom(bgCtx).Info("telegram processing (goroutine fallback)", "chat_id", incoming.ChatID)
		response := s.Telegram.ProcessIncomingTelegram(bgCtx, s.App.(*infra.App), incoming)
		if response == "" {
			response = "I couldn't process that. Please try again."
		}
		if err := s.Telegram.SendMessage(bgCtx, incoming.ChatID, response); err != nil {
			infra.LoggerFrom(bgCtx).Error("telegram reply failed", "chat_id", incoming.ChatID, "error", err)
		} else {
			infra.LoggerFrom(bgCtx).Info("telegram reply sent", "chat_id", incoming.ChatID, "preview", utils.TruncateString(response, 60))
		}
	}()
}
