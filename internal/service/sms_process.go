package service

import (
	"context"
	"strings"

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

	if strings.HasPrefix(text, "?") {
		query := strings.TrimSpace(strings.TrimPrefix(text, "?"))
		if query == "" {
			return "Please include a question after the ?"
		}
		return processQuerySMS(ctx, query, msg.From)
	}

	return processEntrySMS(ctx, text, msg.From)
}

func processQuerySMS(ctx context.Context, query, from string) string {
	ctx, span := infra.StartSpan(ctx, "twilio.process_query")
	defer span.End()

	result := RunQuery(ctx, query, "sms")
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

	id, err := (ServiceEnv{}).AddEntryAndEnqueue(ctx, text, "sms", nil)
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to store entry", "error", err)
		infra.ErrorsTotal.Inc()
		return "Failed to save entry. Please try again."
	}

	infra.LoggerFrom(ctx).Info("entry created via SMS", "id", id, "content_length", len(text))
	return "Noted!"
}
