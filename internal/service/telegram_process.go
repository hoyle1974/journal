package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/pkg/telegram"
)

// ProcessIncomingTelegram processes an incoming Telegram message and returns the response. app must be non-nil.
func ProcessIncomingTelegram(ctx context.Context, app *infra.App, msg *telegram.IncomingMessage) string {
	ctx, span := infra.StartSpan(ctx, "telegram.process_message")
	defer span.End()

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return "Empty message received."
	}

	infra.LoggerFrom(ctx).Info("processing incoming Telegram",
		"chat_id", msg.ChatID,
		"user_id", msg.UserID,
		"update_id", msg.UpdateID,
		"body_length", len(text),
	)

	// Check for an active pending question before running FOH.
	if response, handled := handlePendingQuestion(ctx, app, msg.ChatID, text); handled {
		return response
	}

	return processQueryTelegram(ctx, app, text, msg.ChatID)
}

// handlePendingQuestion checks if there is an active or queued pending question for this
// chat and handles the message as a question answer if so. Returns (response, true) when
// the message was consumed by the question flow, or ("", false) to let FOH run normally.
func handlePendingQuestion(ctx context.Context, app *infra.App, chatID int64, text string) (string, bool) {
	ctx, span := infra.StartSpan(ctx, "telegram.handle_pending_question")
	defer span.End()

	isSkip := text == "/skip" || strings.EqualFold(text, "skip")

	// Check for an in-flight question (one we already sent to this chat).
	clientID := fmt.Sprintf("%d", chatID)
	active, err := app.Memory.GetActiveQuestion(ctx, clientID)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("telegram: could not check active question, proceeding to FOH", "chat_id", chatID, "error", err)
		return "", false
	}

	if active != nil {
		if !isSkip {
			// Resolve the active question with the user's answer.
			if resolveErr := app.Memory.ResolvePendingQuestion(ctx, active.UUID, text); resolveErr != nil {
				infra.LoggerFrom(ctx).Error("telegram: resolve pending question failed", "chat_id", chatID, "uuid", active.UUID, "error", resolveErr)
			} else {
				infra.LoggerFrom(ctx).Info("telegram: pending question resolved", "chat_id", chatID, "uuid", active.UUID)
			}
		} else {
			infra.LoggerFrom(ctx).Info("telegram: pending question skipped", "chat_id", chatID, "uuid", active.UUID)
		}
		// Clear the in-flight state regardless of skip or answer.
		if clearErr := app.Memory.ClearActiveQuestion(ctx, clientID); clearErr != nil {
			infra.LoggerFrom(ctx).Warn("telegram: clear active question failed", "chat_id", chatID, "error", clearErr)
		}
		// Check if there are more questions to ask.
		return askNextOrDone(ctx, app, chatID)
	}

	// No active question — check if there are any unresolved questions at all.
	questions, err := app.Memory.GetUnresolvedPendingQuestions(ctx, 1)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("telegram: could not check pending questions, proceeding to FOH", "chat_id", chatID, "error", err)
		return "", false
	}
	if len(questions) == 0 {
		return "", false // nothing to ask, run FOH
	}

	// Ask the first pending question and store it as active.
	q := questions[0]
	if setErr := app.Memory.SetActiveQuestion(ctx, clientID, q.UUID); setErr != nil {
		infra.LoggerFrom(ctx).Warn("telegram: set active question failed, proceeding to FOH", "chat_id", chatID, "error", setErr)
		return "", false
	}
	infra.LoggerFrom(ctx).Info("telegram: asking pending question", "chat_id", chatID, "uuid", q.UUID, "kind", q.Kind)
	return formatQuestion(q, len(questions)), true
}

// askNextOrDone checks for the next unresolved question. If one exists, asks it and
// returns the formatted prompt. If none remain, returns a "all done" confirmation.
func askNextOrDone(ctx context.Context, app *infra.App, chatID int64) (string, bool) {
	questions, err := app.Memory.GetUnresolvedPendingQuestions(ctx, 1)
	if err != nil || len(questions) == 0 {
		return "Got it! You're all set.", true
	}
	q := questions[0]
	clientID := fmt.Sprintf("%d", chatID)
	if setErr := app.Memory.SetActiveQuestion(ctx, clientID, q.UUID); setErr != nil {
		infra.LoggerFrom(ctx).Warn("telegram: set next active question failed", "chat_id", chatID, "error", setErr)
		return "Got it! You're all set.", true
	}
	infra.LoggerFrom(ctx).Info("telegram: asking next pending question", "chat_id", chatID, "uuid", q.UUID)
	return formatQuestion(q, len(questions)), true
}

// formatQuestion returns a human-readable prompt for the pending question.
// remaining is the total count of unresolved questions (including this one).
func formatQuestion(q memory.PendingQuestion, remaining int) string {
	var sb strings.Builder
	if remaining > 1 {
		sb.WriteString(fmt.Sprintf("I have %d questions for you. Here's the first:\n\n", remaining))
	}
	sb.WriteString(q.Question)
	if q.Context != "" {
		sb.WriteString(fmt.Sprintf("\n\n_%s_", q.Context))
	}
	sb.WriteString("\n\n(Reply to answer, or send /skip to skip.)")
	return sb.String()
}

func processQueryTelegram(ctx context.Context, app *infra.App, query string, chatID int64) string {
	ctx, span := infra.StartSpan(ctx, "telegram.process_query")
	defer span.End()

	if app == nil {
		return "Service unavailable. Please try again later."
	}
	result := RunQuery(ctx, app, query, "telegram")
	if result.Error {
		infra.LoggerFrom(ctx).Error("telegram query failed", "answer", result.Answer)
		return "Sorry, I couldn't process your query. Please try again."
	}
	return result.Answer
}
