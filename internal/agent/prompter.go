package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/utils"
)

// BuildSystemPrompt creates the system prompt with current date context and recent history.
// env supplies Firestore for journal queries; pass from the caller (e.g. FOHEnv).
// ragContext is the 2-hop Loom RAG context for the current input; pass "" to omit the section.
// For which parts are static vs dynamic (context caching), see docs/context-caching-analysis.md.
func BuildSystemPrompt(ctx context.Context, env infra.ToolEnv, ragContext string) (string, error) {
	now := time.Now()
	today := now.Format("2006-01-02")
	currentTime := now.Format("15:04 MST")
	_, week := now.ISOWeek()
	currentWeek := fmt.Sprintf("%d-W%02d", now.Year(), week)
	lastWeek := now.AddDate(0, 0, -7)
	_, lastWeekNum := lastWeek.ISOWeek()
	lastWeekStr := fmt.Sprintf("%d-W%02d", lastWeek.Year(), lastWeekNum)
	currentMonth := now.Format("2006-01")

	identityWrapped := ""

	// 1. Recent Conversation
	var queries []memory.QueryLog
	if env != nil {
		queries, _ = env.MemoryStore().GetRecentQueries(ctx, 5)
	}
	recentConversation := formatConversationSection(queries)
	recentConversationWrapped := recentConversation
	if recentConversation != "" {
		recentConversationWrapped = utils.WrapAsUserData(recentConversation)
	}

	// 2. Proactive Alerts
	proactiveSignals := ""
	proactiveSignalsWrapped := ""
	if signals, err := env.MemoryStore().GetActiveSignals(ctx, 3); err == nil && signals != "" {
		proactiveSignals = formatAlertsSection(signals)
		proactiveSignalsWrapped = utils.WrapAsUserData(proactiveSignals)
	}

	sourceCodeBlock := prompts.SourceCodeBlock()

	// 3. Knowledge Gaps
	var gapQueries []memory.QueryLog
	if env != nil {
		gapQueries, _ = env.MemoryStore().GetRecentGapQueries(ctx, 3)
	}
	knowledgeGapBlock := formatKnowledgeGapSection(gapQueries)
	knowledgeGapBlockWrapped := knowledgeGapBlock
	if knowledgeGapBlock != "" {
		knowledgeGapBlockWrapped = utils.WrapAsUserData(knowledgeGapBlock)
	}

	// 4. Active Project (most recently created root task that has subtasks)
	roots, _ := env.MemoryTasks().GetOpenRootTasks(ctx, 15)
	activeProjectBlock := buildActiveProjectBlock(ctx, env, roots)
	activeProjectBlockWrapped := activeProjectBlock
	if activeProjectBlock != "" {
		activeProjectBlockWrapped = utils.WrapAsUserData(activeProjectBlock)
	}

	// 6. Loom RAG context (2-hop graph expansion of nodes from the current input)
	loomContextBlock := ""
	if ragContext != "" {
		loomContextBlock = utils.WrapAsUserData(ragContext)
	}

	// Log the actual injected context at Info so it appears in production (e.g. tail.sh / LLM_CONTEXT_SENT).
	injectedSections := strings.TrimSpace(recentConversation + proactiveSignals + knowledgeGapBlock + activeProjectBlock)
	if injectedSections == "" {
		injectedSections = "(no dynamic sections)"
	}
	infra.LoggerFrom(ctx).Info("LLM_CONTEXT_SENT | injected context sections (Conversation, Alerts, Gaps, Tasks, Project)",
		"event", "LLM_CONTEXT_SENT",
		"context_sections", injectedSections)

	promptData := prompts.SystemPromptData{
		DelimOpen:          utils.UserDataDelimOpen,
		DelimClose:         utils.UserDataDelimClose,
		SourceCodeBlock:    sourceCodeBlock,
		Today:              today,
		CurrentTime:        currentTime,
		CurrentWeek:        currentWeek,
		LastWeek:           lastWeekStr,
		CurrentMonth:       currentMonth,
		IdentityBlock:      identityWrapped,
		RecentConversation: recentConversationWrapped,
		ProactiveSignals:   proactiveSignalsWrapped,
		KnowledgeGapBlock:  knowledgeGapBlockWrapped,
		ActiveProjectBlock: activeProjectBlockWrapped,
		LoomContextBlock:   loomContextBlock,
	}
	prompt, err := prompts.BuildSystemPrompt(promptData)
	if err != nil {
		return "", fmt.Errorf("build system prompt: %w", err)
	}

	infra.LoggerFrom(ctx).Debug("system prompt built", "prompt_len", len(prompt), "reason", "inject date, recent conversation, signals, gap block, open todo roots")
	return prompt, nil
}

// formatConversationSection builds the RECENT CONVERSATION block with --- and ## header; HH:MM, User/Asst lines.
func formatConversationSection(queries []memory.QueryLog) string {
	if len(queries) == 0 {
		return ""
	}
	var lines []string
	for i := len(queries) - 1; i >= 0; i-- {
		q := queries[i]
		ts := q.Timestamp
		if len(ts) > 16 {
			ts = ts[11:16] // HH:MM for readability
		}
		lines = append(lines, fmt.Sprintf("%s User: %s\n      Asst: %s", ts, utils.TruncateString(q.Question, 100), utils.TruncateString(q.Answer, 150)))
	}
	return fmt.Sprintf("\n---\n## 💬 RECENT CONVERSATION\n# Last 5 exchanges for reference and pronoun resolution.\n\n%s", strings.Join(lines, "\n"))
}

// formatAlertsSection builds the PROACTIVE ALERTS block with --- and ## header.
func formatAlertsSection(signals string) string {
	if signals == "" {
		return ""
	}
	return fmt.Sprintf("\n---\n## 🔔 PROACTIVE ALERTS\n# Mention these if relevant to the current conversation.\n\n%s", strings.TrimSpace(signals))
}

// formatKnowledgeGapSection builds the KNOWLEDGE GAPS block with --- and ## header.
func formatKnowledgeGapSection(gapQueries []memory.QueryLog) string {
	if len(gapQueries) == 0 {
		return ""
	}
	var gaps []string
	for _, g := range gapQueries {
		gaps = append(gaps, "- "+utils.TruncateString(g.Question, 120))
	}
	return fmt.Sprintf("\n---\n## ⚠️ KNOWLEDGE GAPS\n# We looked but found nothing for these; if the user provides information that fills one, save it immediately.\n\n%s", strings.Join(gaps, "\n"))
}

// formatTodoSection builds the OPEN TASKS (ROOT) block with --- and ## header; full task UUID + content.
// buildActiveProjectBlock finds the most recently created open root task that has pending subtasks and
// formats a PROJECT STATUS block for injection into the system prompt.
// Checks up to the first 3 root tasks to bound the number of Firestore calls.
func buildActiveProjectBlock(ctx context.Context, env infra.ToolEnv, roots []memory.Task) string {
	limit := 15
	if len(roots) < limit {
		limit = len(roots)
	}
	for i := 0; i < limit; i++ {
		parent := roots[i]
		children, err := env.MemoryStore().GetChildTasks(ctx, parent.UUID, 10)
		if err != nil || len(children) == 0 {
			continue
		}
		// Found a project with active subtasks — format the block.
		var lines []string
		lines = append(lines, fmt.Sprintf("Project: %s (ID: %s)", parent.Content, parent.UUID))
		for _, c := range children {
			dep := ""
			if len(c.Dependencies) > 0 {
				dep = fmt.Sprintf(" [depends: %s]", strings.Join(c.Dependencies, ", "))
			}
			lines = append(lines, fmt.Sprintf("  - [%s] [%s] %s%s", c.UUID, c.Status, c.Content, dep))
		}
		return fmt.Sprintf("\n---\n## 🚀 ACTIVE PROJECT\n# Most recent project with open subtasks. Check subtasks before asking for clarification.\n\n%s", strings.Join(lines, "\n"))
	}
	return ""
}
