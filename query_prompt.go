package jot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/prompts"
)

// buildSystemPrompt creates the system prompt with current date context and recent history.
func buildSystemPrompt(ctx context.Context) string {
	now := time.Now()
	today := now.Format("2006-01-02")
	_, week := now.ISOWeek()
	currentWeek := fmt.Sprintf("%d-W%02d", now.Year(), week)
	lastWeek := now.AddDate(0, 0, -7)
	_, lastWeekNum := lastWeek.ISOWeek()
	lastWeekStr := fmt.Sprintf("%d-W%02d", lastWeek.Year(), lastWeekNum)
	currentMonth := now.Format("2006-01")

	activeContextsStr := ""
	if contexts, metas, err := GetActiveContexts(ctx, 5); err == nil && len(contexts) > 0 {
		var lines []string
		for i, c := range contexts {
			meta := metas[i]
			if meta.Relevance < 0.4 {
				continue
			}
			content := c.Content
			if strings.EqualFold(meta.ContextName, "user_profile") || strings.EqualFold(meta.ContextName, "system_evolution") || meta.Relevance > 0.75 {
				lines = append(lines, fmt.Sprintf("[HIGH] %s: %s", meta.ContextName, content))
			} else {
				tldr := firstSentence(content, 80)
				lines = append(lines, fmt.Sprintf("[MED] %s: %s", meta.ContextName, tldr))
			}
		}
		if len(lines) > 0 {
			activeContextsStr = fmt.Sprintf(`

ACTIVE CONTEXTS (ongoing projects/plans the user is working on):
%s
Connect new entries to these contexts when relevant.`, WrapAsUserData(strings.Join(lines, "\n")))
		}
	}

	recentContext := ""
	if entries, err := GetEntries(ctx, 5); err == nil && len(entries) > 0 {
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
%s`, len(entries), WrapAsUserData(strings.Join(lines, "\n")))
	}

	recentConversation := ""
	if queries, err := GetRecentQueries(ctx, 3); err == nil && len(queries) > 0 {
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
%s`, WrapAsUserData(strings.Join(lines, "\n\n")))
	}

	proactiveSignals := ""
	if signals, err := GetActiveSignals(ctx, 3); err == nil && signals != "" {
		proactiveSignals = fmt.Sprintf(`

PROACTIVE ALERTS (Mention these if relevant to the current conversation):
%s`, WrapAsUserData(signals))
	}

	sourceCodeBlock := prompts.SourceCodeBlock()

	knowledgeGapBlock := ""
	if gapQueries, err := GetRecentGapQueries(ctx, 5); err == nil && len(gapQueries) > 0 {
		var gapLines []string
		for _, q := range gapQueries {
			question := q.Question
			if len(question) > 120 {
				question = question[:117] + "..."
			}
			gapLines = append(gapLines, "- "+question)
		}
		knowledgeGapBlock = prompts.FormatKnowledgeGap(WrapAsUserData(strings.Join(gapLines, "\n")))
	}

	return fmt.Sprintf(prompts.SystemPromptTemplate(), UserDataDelimOpen, UserDataDelimClose, today, currentWeek, lastWeekStr, currentMonth, activeContextsStr, recentContext, recentConversation, proactiveSignals, knowledgeGapBlock, sourceCodeBlock)
}
