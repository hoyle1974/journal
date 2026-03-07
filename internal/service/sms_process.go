package service

import (
	"context"
	"strings"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
)

// ProcessIncomingSMS processes an incoming SMS and returns a response.
func ProcessIncomingSMS(ctx context.Context, msg *infra.TwilioWebhookRequest) string {
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
	return processQuerySMS(ctx, text, msg.From)
}

func processQuerySMS(ctx context.Context, query, from string) string {
	ctx, span := infra.StartSpan(ctx, "twilio.process_query")
	defer span.End()

	app := infra.GetApp(ctx)
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

func processEntrySMS(ctx context.Context, text, from string) string {
	ctx, span := infra.StartSpan(ctx, "twilio.process_entry")
	defer span.End()

	infra.EntriesTotal.Inc()

	app := infra.GetApp(ctx)
	if app == nil {
		infra.LoggerFrom(ctx).Error("no app in context for SMS entry")
		return "Service unavailable. Please try again later."
	}
	ctx = infra.WithApp(ctx, app)
	id, err := agent.AddEntryAndEnqueue(ctx, text, "sms", nil)
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to store entry", "error", err)
		infra.ErrorsTotal.Inc()
		return "Failed to save entry. Please try again."
	}

	infra.LoggerFrom(ctx).Info("entry created via SMS", "id", id, "content_length", len(text))
	return "Noted!"
}
