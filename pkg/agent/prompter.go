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
	"github.com/jackstrohm/jot/pkg/task"
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

	// 0. Root Identity (always inject user_profile so the model knows who it is serving)
	identityBlock := ""
	if node, _, err := memory.FindContextByName(ctx, "user_profile"); err == nil && node != nil && node.Content != "" {
		identityBlock = "\n---\n## ROOT IDENTITY (who you are serving)\n# Primary user and context. Use this as the authority for preferences and priorities.\n\n" + strings.TrimSpace(node.Content)
	}
	identityWrapped := identityBlock
	if identityBlock != "" {
		identityWrapped = utils.WrapAsUserData(identityBlock)
	}

	// 1. Active Contexts
	nodes, metas, _ := memory.GetActiveContexts(ctx, 5)
	activeContextItems := make([]ActiveContextItem, 0, len(nodes))
	for i := range nodes {
		if i >= len(metas) {
			break
		}
		activeContextItems = append(activeContextItems, ActiveContextItem{
			ContextName: metas[i].ContextName,
			Relevance:   metas[i].Relevance,
			Content:     nodes[i].Content,
		})
	}
	activeContextsStr := formatContextSection(activeContextItems)
	activeContextsWrapped := activeContextsStr
	if activeContextsStr != "" {
		activeContextsWrapped = utils.WrapAsUserData(activeContextsStr)
	}

	// 2. Recent Conversation
	queries, _ := journal.GetRecentQueries(ctx, 5)
	recentConversation := formatConversationSection(queries)
	recentConversationWrapped := recentConversation
	if recentConversation != "" {
		recentConversationWrapped = utils.WrapAsUserData(recentConversation)
	}

	// 3. Proactive Alerts
	proactiveSignals := ""
	proactiveSignalsWrapped := ""
	if signals, err := memory.GetActiveSignals(ctx, 3); err == nil && signals != "" {
		proactiveSignals = formatAlertsSection(signals)
		proactiveSignalsWrapped = utils.WrapAsUserData(proactiveSignals)
	}

	sourceCodeBlock := prompts.SourceCodeBlock()

	// 4. Knowledge Gaps
	gapQueries, _ := journal.GetRecentGapQueries(ctx, 3)
	knowledgeGapBlock := formatKnowledgeGapSection(gapQueries)
	knowledgeGapBlockWrapped := knowledgeGapBlock
	if knowledgeGapBlock != "" {
		knowledgeGapBlockWrapped = utils.WrapAsUserData(knowledgeGapBlock)
	}

	// 5. Open Tasks (root)
	roots, _ := task.GetOpenRootTasks(ctx, 15)
	openTodoBlock := formatTodoSection(roots)
	openTodoBlockWrapped := openTodoBlock
	if openTodoBlock != "" {
		openTodoBlockWrapped = utils.WrapAsUserData(openTodoBlock)
	}

	// Log the actual injected context at Info so it appears in production (e.g. tail.sh / LLM_CONTEXT_SENT).
	injectedSections := strings.TrimSpace(identityBlock + activeContextsStr + recentConversation + proactiveSignals + knowledgeGapBlock + openTodoBlock)
	if injectedSections == "" {
		injectedSections = "(no dynamic sections)"
	}
	infra.LoggerFrom(ctx).Info("LLM_CONTEXT_SENT | injected context sections (Contexts, Conversation, Alerts, Gaps, Tasks)",
		"event", "LLM_CONTEXT_SENT",
		"context_sections", injectedSections)

	// Template order: preamble (cacheable) then ======= then dynamic. Placeholders: delimOpen, delimClose, sourceCodeBlock, today, currentWeek, lastWeekStr, currentMonth, identity, activeContextsStr, recentConversation, proactiveSignals, knowledgeGapBlock, openTodoBlock.
	prompt := fmt.Sprintf(prompts.SystemPromptTemplate(), utils.UserDataDelimOpen, utils.UserDataDelimClose, sourceCodeBlock, today, currentWeek, lastWeekStr, currentMonth, identityWrapped, activeContextsWrapped, recentConversationWrapped, proactiveSignalsWrapped, knowledgeGapBlockWrapped, openTodoBlockWrapped)

	// Map vs Manual: compressed manifest + 3 core tools (semantic_search, upsert_knowledge, discovery_search). Everything else via discovery_search(intent) → JIT schema injection.
	if app := infra.GetApp(ctx); app != nil && app.Config() != nil && app.Config().UseCompactTools {
		prompt += "\n\n---\n## TOOLS (Map)\nYou have access to tool suites: journaling, task_management, knowledge_graph, web_research.\nYou have these tools always available: semantic_search, upsert_knowledge, discovery_search.\nFor any other action (create task, search journal, wikipedia, etc.), first call discovery_search(intent=\"your_reasoning\") to receive the specific tool schemas; then invoke that tool with a JSON block: {\"tool\": \"tool_name\", \"args\": {\"param\": \"value\", ...}}. Do not output any other text when making a tool call."
		infra.LoggerFrom(ctx).Debug("system prompt: Map vs Manual (core tools + discovery)", "reason", "JOT_USE_COMPACT_TOOLS=true")
	}

	infra.LoggerFrom(ctx).Debug("system prompt built", "prompt_len", len(prompt), "reason", "inject date, active contexts, recent conversation, signals, gap block, open todo roots")
	return prompt
}

// formatContextSection builds the ACTIVE CONTEXTS block with --- and ## header; tag by name/relevance, Briefing for content.
func formatContextSection(items []ActiveContextItem) string {
	if len(items) == 0 {
		return ""
	}
	var lines []string
	for _, item := range items {
		if item.Relevance < 0.4 {
			continue
		}
		tag := "PROJECT"
		if strings.Contains(item.ContextName, "user_") {
			tag = "IDENTITY"
		}
		if item.Relevance > 0.8 {
			tag = "CRITICAL"
		}
		content := utils.FirstSentence(item.Content, 150)
		lines = append(lines, fmt.Sprintf("- [%s] %s (Rel: %.0f%%)\n  Briefing: %s", tag, item.ContextName, item.Relevance*100, content))
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("\n---\n## 🧠 ACTIVE CONTEXTS\n# High-relevance project briefings and situational awareness.\n\n%s", strings.Join(lines, "\n"))
}

// formatConversationSection builds the RECENT CONVERSATION block with --- and ## header; HH:MM, User/Asst lines.
func formatConversationSection(queries []journal.QueryLog) string {
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
func formatKnowledgeGapSection(gapQueries []journal.QueryLog) string {
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
// Full UUIDs are required so update_task_status/update_task find the Firestore document (doc ID is the full UUID).
func formatTodoSection(roots []task.Task) string {
	if len(roots) == 0 {
		return ""
	}
	var lines []string
	for _, t := range roots {
		lines = append(lines, fmt.Sprintf("- [%s] %s", t.UUID, t.Content))
	}
	return fmt.Sprintf("\n---\n## ✅ OPEN TASKS (ROOT)\n# Primary pending actions from the operation queue.\n\n%s", strings.Join(lines, "\n"))
}
