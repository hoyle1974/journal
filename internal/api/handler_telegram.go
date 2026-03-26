package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

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

	taskID := "process-telegram-query-" + infra.GenShortRunID()
	parentTraceID := infra.TraceIDFromContext(ctx)
	infra.LoggerFrom(ctx).Debug("telegram webhook: submitting for background processing", "chat_id", incoming.ChatID, "task_id", taskID)
	msgCopy := *incoming
	s.App.SubmitAsync(func() {
		bgCtx := s.App.WithContext(context.Background())
		bgCtx = infra.WithCorrelation(bgCtx, taskID, parentTraceID)
		bgCtx, cancel := context.WithTimeout(bgCtx, 120*time.Second)
		defer cancel()
		if err := runTelegramMessage(bgCtx, s, &msgCopy); err != nil {
			infra.LoggerFrom(bgCtx).Error("telegram processing failed", "chat_id", msgCopy.ChatID, "error", err)
		}
	})
}
