package service

import (
	"context"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/sms"
)

// ProcessIncomingSMS processes an incoming SMS and returns a response. app must be non-nil.
func ProcessIncomingSMS(ctx context.Context, app *infra.App, msg *sms.TwilioWebhookRequest) string {
	ctx, span := infra.StartSpan(ctx, "twilio.process_sms")
	defer span.End()

	text := strings.TrimSpace(msg.Body)
	if text == "" {
		return "Empty message received."
	}

	infra.LoggerFrom(ctx).Info("processing incoming SMS",
		"from", msg.From,
		"message_sid", msg.MessageSid,
		"body_length", len(text),
	)

	// Same as jot app default: plain text is always treated as a query (FOH). No "?" required.
	return processQuerySMS(ctx, app, text, msg.From)
}

func processQuerySMS(ctx context.Context, app *infra.App, query, from string) string {
	ctx, span := infra.StartSpan(ctx, "twilio.process_query")
	defer span.End()

	if app == nil {
		return "Service unavailable. Please try again later."
	}
	result := RunQuery(ctx, app, query, "sms")
	if result.Error {
		infra.LoggerFrom(ctx).Error("query failed", "answer", result.Answer)
		return "Sorry, I couldn't process your query. Please try again."
	}
	return result.Answer
}
