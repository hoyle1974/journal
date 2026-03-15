package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

func handleSMS(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	ctx, span := infra.StartSpan(ctx, "sms.webhook")
	defer span.End()
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	webhookURL := fmt.Sprintf("https://us-central1-%s.cloudfunctions.net/jot-api-go/sms", s.Config.GoogleCloudProject)
	if !s.SMS.ValidateTwilioSignature(r, webhookURL) {
		infra.LoggerFrom(ctx).Warn("invalid Twilio signature")
		LogHandlerResponse(ctx, r.Method, path, http.StatusUnauthorized, "error", "Invalid signature")
		WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid signature"})
		return
	}
	msg, err := s.SMS.ParseTwilioWebhook(r)
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to parse Twilio webhook", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "Invalid request")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}
	span.SetAttributes(map[string]string{"sms.from": msg.From, "sms.message_sid": msg.MessageSid})
	bodyPreview := strings.TrimSpace(msg.Body)
	if bodyPreview == "" {
		bodyPreview = "(empty)"
	} else {
		bodyPreview = utils.TruncateString(bodyPreview, 80)
	}
	LogHandlerRequest(ctx, r.Method, path, "from", msg.From, "sid", msg.MessageSid, "body_preview", bodyPreview)
	if !s.SMS.IsAllowedPhoneNumber(msg.From) {
		infra.LoggerFrom(ctx).Warn("SMS from unauthorized number", "from", msg.From)
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ignored", "reason", "unauthorized number")
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "accepted", "from", msg.From)
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response><Message>Got it!</Message></Response>`))
	infra.LoggerFrom(ctx).Info("sms responded 200, processing in background")

	// Prefer Cloud Task so the query runs in a request-scoped context (Cloud Run keeps that request alive until the reply is sent).
	// Response delivery: task handler runs FOH and sends the answer via SendSMS to the user's phone.
	taskID := "process-sms-query-" + infra.GenShortRunID()
	parentTraceID := infra.TraceIDFromContext(ctx)
	payload := map[string]interface{}{
		"from":            msg.From,
		"body":            msg.Body,
		"message_sid":     msg.MessageSid,
		"task_id":         taskID,
		"parent_trace_id": parentTraceID,
	}
	enqErr := s.App.EnqueueTask(ctx, "/internal/process-sms-query", payload)
	if enqErr == nil {
		infra.LoggerFrom(ctx).Debug("sms enqueued for process-sms-query", "from", msg.From, "task_id", taskID, "parent_trace_id", parentTraceID)
		return
	}
	// Fallback: run in goroutine when Cloud Tasks unavailable (e.g. local dev) so we still deliver the reply.
	infra.LoggerFrom(ctx).Warn("sms task enqueue failed, processing in goroutine", "from", msg.From, "error", enqErr)
	go func() {
		bgCtx := s.App.WithContext(context.Background())
		bgCtx = infra.WithCorrelation(bgCtx, taskID, parentTraceID)
		infra.LoggerFrom(bgCtx).Info("sms processing (goroutine fallback)", "from", msg.From)
		response := s.SMS.ProcessIncomingSMS(bgCtx, s.App.(*infra.App), msg)
		if response == "" {
			response = "I couldn't process that. Please try again."
		}
		if err := s.SMS.SendSMS(bgCtx, msg.From, response); err != nil {
			infra.LoggerFrom(bgCtx).Error("sms reply failed", "to", msg.From, "error", err)
		} else {
			infra.LoggerFrom(bgCtx).Info("sms reply sent", "to", msg.From, "preview", utils.TruncateString(response, 60))
		}
	}()
}
