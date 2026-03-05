package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

func handleSMS(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx, span := infra.StartSpan(ctx, "sms.webhook")
	defer span.End()
	if r.Method != http.MethodPost {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	webhookURL := fmt.Sprintf("https://us-central1-%s.cloudfunctions.net/jot-api-go/sms", s.Config.GoogleCloudProject)
	if !s.Backend.ValidateTwilioSignature(r, webhookURL) {
		infra.LoggerFrom(ctx).Warn("invalid Twilio signature")
		WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid signature"})
		return
	}
	msg, err := s.Backend.ParseTwilioWebhook(r)
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to parse Twilio webhook", "error", err)
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}
	span.SetAttributes(map[string]string{"sms.from": msg.From, "sms.message_sid": msg.MessageSid})
	if !s.Backend.IsAllowedPhoneNumber(msg.From) {
		infra.LoggerFrom(ctx).Warn("SMS from unauthorized number", "from", msg.From)
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))
		return
	}
	bodyPreview := strings.TrimSpace(msg.Body)
	if bodyPreview == "" {
		bodyPreview = "(empty)"
	} else {
		bodyPreview = utils.TruncateString(bodyPreview, 80)
	}
	infra.LoggerFrom(ctx).Info("sms webhook", "from", msg.From, "sid", msg.MessageSid, "body", bodyPreview)
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))
	infra.LoggerFrom(ctx).Info("sms responded 200, processing in background")
	go func() {
		bgCtx := context.Background()
		infra.LoggerFrom(bgCtx).Info("sms processing", "from", msg.From)
		response := s.Backend.ProcessIncomingSMS(bgCtx, msg)
		if response != "" {
			if err := s.Backend.SendSMS(bgCtx, msg.From, response); err != nil {
				infra.LoggerFrom(bgCtx).Error("sms reply failed", "to", msg.From, "error", err)
			} else {
				infra.LoggerFrom(bgCtx).Info("sms reply sent", "to", msg.From, "preview", utils.TruncateString(response, 60))
			}
		} else {
			infra.LoggerFrom(bgCtx).Info("sms processed", "from", msg.From, "reply", "none")
		}
	}()
}
