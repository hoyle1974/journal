package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
)

const telegramHelpText = `Available commands:

/dream — Run memory consolidation (extracts facts from the last 24h of entries)
/recall — Show the latest dream narrative
/help — Show this message
/skip — Skip the current pending question`

// handleTelegramSlashCommand dispatches known slash commands. Returns (true, err) when the command
// was handled (caller should return early). Returns (false, nil) for unknown commands, which
// fall through to FOH processing.
func handleTelegramSlashCommand(ctx context.Context, s *Server, chatID int64, text string) (bool, error) {
	cmd := strings.ToLower(strings.Fields(text)[0])
	switch cmd {
	case "/dream":
		return true, handleTelegramDream(ctx, s, chatID)
	case "/recall":
		return true, handleTelegramRecall(ctx, s, chatID)
	case "/help":
		return true, handleTelegramHelp(ctx, s, chatID)
	}
	return false, nil
}

// handleTelegramDream triggers a dream run and streams progress to the Telegram chat.
func handleTelegramDream(ctx context.Context, s *Server, chatID int64) error {
	ctx, span := infra.StartSpan(ctx, "telegram.slash_dream")
	defer span.End()

	runID := infra.GenShortRunID()
	acquired, existingRunID, err := s.System.TryAcquireDreamRunLock(ctx, runID)
	if err != nil {
		infra.LoggerFrom(ctx).Error("telegram /dream: lock check failed", "chat_id", chatID, "error", err)
		return s.Telegram.SendMessage(ctx, chatID, "Could not start dream run. Please try again.")
	}
	if !acquired {
		infra.LoggerFrom(ctx).Info("telegram /dream: already running", "chat_id", chatID, "existing_run_id", existingRunID)
		return s.Telegram.SendMessage(ctx, chatID, fmt.Sprintf("A dream run is already in progress (run ID: %s). Check back soon.", existingRunID))
	}

	if err := s.Telegram.SendMessage(ctx, chatID, "Dream run starting... I'll send updates as it runs."); err != nil {
		infra.LoggerFrom(ctx).Warn("telegram /dream: send ack failed", "chat_id", chatID, "error", err)
	}

	bgCtx := s.App.WithContext(context.Background())
	parentTraceID := infra.TraceIDFromContext(ctx)
	bgCtx = infra.WithCorrelation(bgCtx, runID, parentTraceID)
	progress := &telegramDreamerProgress{
		chatID:   chatID,
		telegram: s.Telegram,
	}

	go func() {
		runCtx, cancel := context.WithTimeout(bgCtx, 55*time.Minute)
		defer cancel()
		infra.LoggerFrom(runCtx).Info("telegram /dream: goroutine starting", "chat_id", chatID, "dream_run_id", runID)
		result, err := s.Agent.RunDreamerWithProgress(runCtx, runID, progress)
		if err != nil {
			infra.LoggerFrom(runCtx).Error("telegram /dream: run failed", "chat_id", chatID, "dream_run_id", runID, "error", err)
			_ = s.System.SetDreamRunFailed(runCtx, runID, err.Error())
			_ = s.Telegram.SendMessage(runCtx, chatID, fmt.Sprintf("Dream run failed: %s", err.Error()))
			return
		}
		_ = s.System.SetDreamRunCompleted(runCtx, runID, map[string]interface{}{
			"entries_processed":    result.EntriesProcessed,
			"facts_extracted":      result.FactsExtracted,
			"facts_written":        result.FactsWritten,
			"contexts_synthesized": result.ContextsSynthesized,
		})
		summary := fmt.Sprintf(
			"Dream complete.\n- Entries processed: %d\n- Facts written: %d\n- Contexts synthesized: %d",
			result.EntriesProcessed, result.FactsWritten, result.ContextsSynthesized,
		)
		infra.LoggerFrom(runCtx).Info("telegram /dream: completed", "chat_id", chatID, "dream_run_id", runID,
			"entries", result.EntriesProcessed, "facts_written", result.FactsWritten)
		_ = s.Telegram.SendMessage(runCtx, chatID, summary)
	}()

	return nil
}

// handleTelegramRecall fetches the latest dream narrative and sends it to the chat.
func handleTelegramRecall(ctx context.Context, s *Server, chatID int64) error {
	ctx, span := infra.StartSpan(ctx, "telegram.slash_recall")
	defer span.End()

	latest, err := s.System.GetLatestDream(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("telegram /recall: get latest dream failed", "chat_id", chatID, "error", err)
		return s.Telegram.SendMessage(ctx, chatID, "Could not retrieve the latest dream narrative. Please try again.")
	}
	if latest == nil || latest.Narrative == "" {
		return s.Telegram.SendMessage(ctx, chatID, "No dream narrative yet. Run /dream to generate one.")
	}
	msg := latest.Narrative
	if latest.Timestamp != "" {
		msg = fmt.Sprintf("_Last run: %s_\n\n%s", latest.Timestamp, latest.Narrative)
	}
	return s.Telegram.SendMessage(ctx, chatID, msg)
}

// handleTelegramHelp sends the list of available slash commands.
func handleTelegramHelp(ctx context.Context, s *Server, chatID int64) error {
	return s.Telegram.SendMessage(ctx, chatID, telegramHelpText)
}

// telegramDreamerProgress implements agent.DreamerProgress by sending phase/log updates to a Telegram chat.
type telegramDreamerProgress struct {
	chatID   int64
	telegram TelegramService
}

func (p *telegramDreamerProgress) OnPhase(ctx context.Context, phase string) {
	_ = p.telegram.SendMessage(ctx, p.chatID, "Phase: "+phase)
}

func (p *telegramDreamerProgress) OnLog(ctx context.Context, msg string) {
	_ = p.telegram.SendMessage(ctx, p.chatID, msg)
}

// Ensure telegramDreamerProgress satisfies the interface.
var _ agent.DreamerProgress = (*telegramDreamerProgress)(nil)
