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
	_, incoming, err := s.Telegram.ParseWebhook(r)
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to parse Telegram webhook", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "Invalid request")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}
	if incoming == nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ignored", "reason", "no message")
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "true"})
		return
	}
	span.SetAttributes(map[string]string{"telegram.chat_id": fmt.Sprintf("%d", incoming.ChatID), "telegram.update_id": fmt.Sprintf("%d", incoming.UpdateID)})
	bodyPreview := incoming.Text
	if bodyPreview == "" {
		bodyPreview = "(empty)"
	} else {
		bodyPreview = utils.TruncateString(bodyPreview, 80)
	}
	LogHandlerRequest(ctx, r.Method, path, "chat_id", incoming.ChatID, "user_id", incoming.UserID, "update_id", incoming.UpdateID, "body_preview", bodyPreview)
	if !s.Telegram.IsAllowedUser(incoming.UserID) {
		infra.LoggerFrom(ctx).Warn("Telegram from unauthorized user", "user_id", incoming.UserID)
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ignored", "reason", "unauthorized user")
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "true"})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "accepted", "chat_id", incoming.ChatID)
	WriteJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	infra.LoggerFrom(ctx).Info("telegram responded 200, processing in background")

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
	enqErr := s.App.EnqueueTask(ctx, "/internal/process-telegram-query", payload)
	if enqErr == nil {
		infra.LoggerFrom(ctx).Debug("telegram enqueued for process-telegram-query", "chat_id", incoming.ChatID, "task_id", taskID, "parent_trace_id", parentTraceID)
		return
	}
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
