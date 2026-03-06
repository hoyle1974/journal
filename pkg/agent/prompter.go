package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/utils"
)

// BuildSystemPrompt creates the system prompt with current date context and recent history.
func BuildSystemPrompt(ctx context.Context, env PrompterEnv) string {
	now := time.Now()
	today := now.Format("2006-01-02")
	_, week := now.ISOWeek()
	currentWeek := fmt.Sprintf("%d-W%02d", now.Year(), week)
	lastWeek := now.AddDate(0, 0, -7)
	_, lastWeekNum := lastWeek.ISOWeek()
	lastWeekStr := fmt.Sprintf("%d-W%02d", lastWeek.Year(), lastWeekNum)
	currentMonth := now.Format("2006-01")

	activeContextsStr := ""
	if items, err := env.GetActiveContexts(ctx, 5); err == nil && len(items) > 0 {
		var lines []string
		for _, item := range items {
			if item.Relevance < 0.4 {
				continue
			}
			content := item.Content
			if strings.EqualFold(item.ContextName, "user_profile") || strings.EqualFold(item.ContextName, "system_evolution") || item.Relevance > 0.75 {
				lines = append(lines, fmt.Sprintf("[HIGH] %s: %s", item.ContextName, content))
			} else {
				tldr := utils.FirstSentence(content, 80)
				lines = append(lines, fmt.Sprintf("[MED] %s: %s", item.ContextName, tldr))
			}
		}
		if len(lines) > 0 {
			activeContextsStr = fmt.Sprintf(`

ACTIVE CONTEXTS (ongoing projects/plans the user is working on):
%s
Connect new entries to these contexts when relevant.`, utils.WrapAsUserData(strings.Join(lines, "\n")))
		}
	}

	recentContext := ""
	if entries, err := journal.GetEntries(ctx, 5); err == nil && len(entries) > 0 {
		var lines []string
		for _, e := range entries {
			ts := e.Timestamp
			if len(ts) > 16 {
				ts = ts[:16]
			}
			content := e.Content
			if len(content) > 150 {
				content = content[:147] + "..."
			}
			lines = append(lines, fmt.Sprintf("- [%s] %s", ts, content))
		}
		recentContext = fmt.Sprintf(`

RECENT HISTORY (last %d entries - ALREADY SAVED, do NOT re-log or re-upsert any of this):
%s`, len(entries), utils.WrapAsUserData(strings.Join(lines, "\n")))
	}

	recentConversation := ""
	if queries, err := journal.GetRecentQueries(ctx, 3); err == nil && len(queries) > 0 {
		var lines []string
		for i := len(queries) - 1; i >= 0; i-- {
			q := queries[i]
			question := q.Question
			if len(question) > 100 {
				question = question[:97] + "..."
			}
			answer := q.Answer
			if len(answer) > 150 {
				answer = answer[:147] + "..."
			}
			ts := q.Timestamp
			if len(ts) > 16 {
				ts = ts[:16]
			}
			lines = append(lines, fmt.Sprintf("[%s] User: %s\nAssistant: %s", ts, question, answer))
		}
		recentConversation = fmt.Sprintf(`

RECENT CONVERSATION (for pronoun resolution only - ALREADY SAVED, do NOT re-log or re-upsert):
%s`, utils.WrapAsUserData(strings.Join(lines, "\n\n")))
	}

	proactiveSignals := ""
	if signals, err := env.GetActiveSignals(ctx, 3); err == nil && signals != "" {
		proactiveSignals = fmt.Sprintf(`

PROACTIVE ALERTS (Mention these if relevant to the current conversation):
%s`, utils.WrapAsUserData(signals))
	}

	sourceCodeBlock := prompts.SourceCodeBlock()

	knowledgeGapBlock := ""
	if gapQueries, err := journal.GetRecentGapQueries(ctx, 5); err == nil && len(gapQueries) > 0 {
		var gapLines []string
		for _, q := range gapQueries {
			question := q.Question
			if len(question) > 120 {
				question = question[:117] + "..."
			}
			gapLines = append(gapLines, "- "+question)
		}
		knowledgeGapBlock = prompts.FormatKnowledgeGap(utils.WrapAsUserData(strings.Join(gapLines, "\n")))
	}

	prompt := fmt.Sprintf(prompts.SystemPromptTemplate(), utils.UserDataDelimOpen, utils.UserDataDelimClose, today, currentWeek, lastWeekStr, currentMonth, activeContextsStr, recentContext, recentConversation, proactiveSignals, knowledgeGapBlock, sourceCodeBlock)
	infra.LoggerFrom(ctx).Debug("system prompt built", "prompt_len", len(prompt), "reason", "inject date, active contexts, recent history, signals, gap block")
	return prompt
}
