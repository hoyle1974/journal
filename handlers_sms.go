package jot

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackstrohm/jot/internal/api"
)

func handleSMS(s *api.Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ctx, span := StartSpan(ctx, "sms.webhook")
	defer span.End()

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	webhookURL := fmt.Sprintf("https://us-central1-%s.cloudfunctions.net/jot-api-go/sms", s.Config.GoogleCloudProject)

	if !ValidateTwilioSignature(r, webhookURL) {
		LoggerFrom(ctx).Warn("invalid Twilio signature")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid signature"})
		return
	}

	msg, err := ParseTwilioWebhook(r)
	if err != nil {
		LoggerFrom(ctx).Error("failed to parse Twilio webhook", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}

	span.SetAttributes(map[string]string{
		"sms.from":        msg.From,
		"sms.message_sid": msg.MessageSid,
	})

	if !IsAllowedPhoneNumber(msg.From) {
		LoggerFrom(ctx).Warn("SMS from unauthorized number", "from", msg.From)
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))
		return
	}

	bodyPreview := strings.TrimSpace(msg.Body)
	if bodyPreview == "" {
		bodyPreview = "(empty)"
	} else {
		bodyPreview = truncateString(bodyPreview, 80)
	}
	LoggerFrom(ctx).Info("sms webhook", "from", msg.From, "sid", msg.MessageSid, "body", bodyPreview)

	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))

	LoggerFrom(ctx).Info("sms responded 200, processing in background")

	go func() {
		bgCtx := context.Background()
		LoggerFrom(bgCtx).Info("sms processing", "from", msg.From)
		response := ProcessIncomingSMS(bgCtx, msg)
		if response != "" {
			if err := SendSMS(bgCtx, msg.From, response); err != nil {
				LoggerFrom(bgCtx).Error("sms reply failed", "to", msg.From, "error", err)
			} else {
				LoggerFrom(bgCtx).Info("sms reply sent", "to", msg.From, "preview", truncateString(response, 60))
			}
		} else {
			LoggerFrom(bgCtx).Info("sms processed", "from", msg.From, "reply", "none")
		}
	}()
}
