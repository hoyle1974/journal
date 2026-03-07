package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/utils"
)

// ActiveContextItem is one context node for the system prompt (name, relevance, content).
type ActiveContextItem struct {
	ContextName string
	Relevance   float64
	Content     string
}

// BuildSystemPrompt creates the system prompt with current date context and recent history.
// For which parts are static vs dynamic (context caching), see docs/context-caching-analysis.md.
func BuildSystemPrompt(ctx context.Context) string {
	now := time.Now()
	today := now.Format("2006-01-02")
	_, week := now.ISOWeek()
	currentWeek := fmt.Sprintf("%d-W%02d", now.Year(), week)
	lastWeek := now.AddDate(0, 0, -7)
	_, lastWeekNum := lastWeek.ISOWeek()
	lastWeekStr := fmt.Sprintf("%d-W%02d", lastWeek.Year(), lastWeekNum)
	currentMonth := now.Format("2006-01")

	activeContextsStr := ""
	nodes, metas, err := memory.GetActiveContexts(ctx, 5)
	if err == nil && len(nodes) > 0 {
		items := make([]ActiveContextItem, 0, len(nodes))
		for i, n := range nodes {
			if i >= len(metas) {
				break
			}
			items = append(items, ActiveContextItem{
				ContextName: metas[i].ContextName,
				Relevance:   metas[i].Relevance,
				Content:     n.Content,
			})
		}
		if len(items) > 0 {
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
	}

	recentConversation := ""
	if queries, err := journal.GetRecentQueries(ctx, 5); err == nil && len(queries) > 0 {
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

RECENT CONVERSATION (last %d Q&A pairs - user question + assistant answer, for pronoun resolution - ALREADY SAVED, do NOT re-log or re-upsert):
%s`, len(queries), utils.WrapAsUserData(strings.Join(lines, "\n\n")))
	}

	proactiveSignals := ""
	if signals, err := memory.GetActiveSignals(ctx, 3); err == nil && signals != "" {
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

	// Template order: preamble (cacheable) then ======= then dynamic. Placeholders: delimOpen, delimClose, sourceCodeBlock, today, currentWeek, lastWeekStr, currentMonth, activeContextsStr, recentConversation, proactiveSignals, knowledgeGapBlock.
	prompt := fmt.Sprintf(prompts.SystemPromptTemplate(), utils.UserDataDelimOpen, utils.UserDataDelimClose, sourceCodeBlock, today, currentWeek, lastWeekStr, currentMonth, activeContextsStr, recentConversation, proactiveSignals, knowledgeGapBlock)
	infra.LoggerFrom(ctx).Debug("system prompt built", "prompt_len", len(prompt), "reason", "inject date, active contexts, recent conversation, signals, gap block")
	return prompt
}
