package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/sms"
	"github.com/jackstrohm/jot/pkg/telegram"
	"github.com/jackstrohm/jot/pkg/utils"
)

func handleProcessEntry(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		UUID          string `json:"uuid" validate:"required"`
		Content       string `json:"content" validate:"required"`
		Timestamp     string `json:"timestamp"`
		Source        string `json:"source" validate:"required"`
		TaskID        string `json:"task_id"`
		ParentTraceID string `json:"parent_trace_id"`
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", err.Error())
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if data.TaskID != "" || data.ParentTraceID != "" {
		ctx = infra.WithCorrelation(ctx, data.TaskID, data.ParentTraceID)
	}
	LogHandlerRequest(ctx, r.Method, path, "uuid", data.UUID, "source", data.Source, "content_length", len(data.Content), "task_id", data.TaskID, "parent_trace_id", data.ParentTraceID)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	breakdown, err := s.Agent.ProcessEntry(ctx, data.UUID, data.Content, data.Timestamp, data.Source)
	if setter, ok := w.(interface{ SetLatencyBreakdown(*infra.LatencyBreakdown) }); ok && breakdown != nil {
		setter.SetLatencyBreakdown(breakdown)
	}
	if err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ok", "uuid", data.UUID)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleProcessSMSQuery runs the query for an incoming SMS (FOH) and sends the reply via Twilio.
// Invoked by a Cloud Task enqueued from the SMS webhook so the work runs in a request-scoped context (Cloud Run keeps the request alive until done).
func handleProcessSMSQuery(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		From          string `json:"from" validate:"required"`
		Body          string `json:"body" validate:"required"`
		MessageSid    string `json:"message_sid"`
		TaskID        string `json:"task_id"`
		ParentTraceID string `json:"parent_trace_id"`
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", err.Error())
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if data.TaskID != "" || data.ParentTraceID != "" {
		ctx = infra.WithCorrelation(ctx, data.TaskID, data.ParentTraceID)
	}
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
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send SMS reply"})
		return
	}
	infra.LoggerFrom(ctx).Info("process-sms-query: reply sent", "to", data.From, "preview", utils.TruncateString(response, 60))
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ok", "to", data.From)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleProcessTelegramQuery runs the query for an incoming Telegram message (FOH) and sends the reply via Telegram.
func handleProcessTelegramQuery(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		ChatID        int64  `json:"chat_id" validate:"required"`
		UserID        int64  `json:"user_id"`
		Body          string `json:"body"`
		ImageFileID   string `json:"image_file_id"`
		UpdateID      int64  `json:"update_id"`
		MessageID     int64  `json:"message_id"`
		TaskID        string `json:"task_id"`
		ParentTraceID string `json:"parent_trace_id"`
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", err.Error())
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if data.Body == "" && data.ImageFileID == "" {
		infra.LoggerFrom(ctx).Info("process-telegram-query: empty body and no image, sending hint", "chat_id", data.ChatID)
		_ = s.Telegram.SendMessage(ctx, data.ChatID, "I didn't receive any text or image. Send a message or photo to log something.")
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ok")
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if data.Body == "" && data.ImageFileID != "" {
		data.Body = "Photo"
	}
	if data.TaskID != "" || data.ParentTraceID != "" {
		ctx = infra.WithCorrelation(ctx, data.TaskID, data.ParentTraceID)
	}
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
				WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send Telegram reply"})
				return
			}
			infra.LoggerFrom(ctx).Info("process-telegram-query: reply sent", "chat_id", data.ChatID, "preview", response)
			LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ok", "chat_id", data.ChatID)
			WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		// Image with generated caption: return the caption to the user and confirm log (skip FOH).
		response := data.Body + "\n\nLogged."
		if err := s.Telegram.SendMessage(ctx, data.ChatID, response); err != nil {
			infra.LoggerFrom(ctx).Error("process-telegram-query: send reply failed", "chat_id", data.ChatID, "error", err)
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send Telegram reply"})
			return
		}
		infra.LoggerFrom(ctx).Info("process-telegram-query: reply sent (caption)", "chat_id", data.ChatID, "preview", utils.TruncateString(response, 60))
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ok", "chat_id", data.ChatID)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	msg := &telegram.IncomingMessage{
		UpdateID:    data.UpdateID,
		MessageID:   data.MessageID,
		ChatID:      data.ChatID,
		UserID:      data.UserID,
		Text:        data.Body,
		ImageFileID: data.ImageFileID,
	}
	LogHandlerRequest(ctx, r.Method, path, "chat_id", data.ChatID, "update_id", data.UpdateID, "body_length", len(data.Body), "task_id", data.TaskID, "parent_trace_id", data.ParentTraceID)
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	response := s.Telegram.ProcessIncomingTelegram(ctx, s.App.(*infra.App), msg)
	if response == "" {
		response = "I couldn't process that. Please try again."
	}
	if err := s.Telegram.SendMessage(ctx, data.ChatID, response); err != nil {
		infra.LoggerFrom(ctx).Error("process-telegram-query: send reply failed", "chat_id", data.ChatID, "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send Telegram reply"})
		return
	}
	infra.LoggerFrom(ctx).Info("process-telegram-query: reply sent", "chat_id", data.ChatID, "preview", utils.TruncateString(response, 60))
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ok", "chat_id", data.ChatID)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleSaveQuery(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		Question      string `json:"question" validate:"required"`
		Answer        string `json:"answer"`
		Source        string `json:"source" validate:"required"`
		IsGap         bool   `json:"is_gap"`
		TaskID        string `json:"task_id"`
		ParentTraceID string `json:"parent_trace_id"`
	}
	if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", err.Error())
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if data.TaskID != "" || data.ParentTraceID != "" {
		ctx = infra.WithCorrelation(ctx, data.TaskID, data.ParentTraceID)
	}
	LogHandlerRequest(ctx, r.Method, path, "question_preview", utils.TruncateString(data.Question, 60), "source", data.Source, "is_gap", data.IsGap, "task_id", data.TaskID, "parent_trace_id", data.ParentTraceID)
	if _, err := s.Journal.SaveQuery(ctx, data.Question, data.Answer, data.Source, data.IsGap); err != nil {
		infra.LoggerFrom(ctx).Error("save-query failed", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ok", "source", data.Source)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleBackfillEmbeddings(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
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
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "success", true, "processed", processed)
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "processed": processed})
}
