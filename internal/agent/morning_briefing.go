package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/memory"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/system"
	"github.com/jackstrohm/jot/pkg/telegram"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/genai"
)

const (
	// briefingMaxEntries caps Gravel log entries passed to the LLM.
	briefingMaxEntries = 50
	// briefingMaxGoldNodes caps active goal/project nodes passed to the LLM.
	briefingMaxGoldNodes = 20
	// briefingFallbackTopN is the number of top active nodes shown in the fallback message.
	briefingFallbackTopN = 3
)

// briefingGoldNode is a lightweight representation of an active goal or project node
// used for formatting and the fallback message.
type briefingGoldNode struct {
	Content           string
	NodeType          string
	Status            string
	SignificanceWeight float64
}

// MorningBriefingResult summarises the outcome of a morning briefing run.
type MorningBriefingResult struct {
	MessageSent string `json:"message_sent,omitempty"`
	Skipped     bool   `json:"skipped,omitempty"`
	SkipReason  string `json:"skip_reason,omitempty"`
}

// RunMorningBriefing performs the Morning Briefing cycle:
// reads the watermark, fetches recent Gravel and active Gold, calls the LLM,
// formats and pushes the briefing to Telegram, then updates the watermark.
//
// When force is false and there are no new Gravel entries, a minimal fallback
// message listing the top active projects is sent instead of a full analysis.
func RunMorningBriefing(ctx context.Context, app *infra.App, force bool) (*MorningBriefingResult, error) {
	if app == nil {
		return nil, fmt.Errorf("morning briefing: app is nil")
	}
	log := infra.LoggerFrom(ctx)
	log.Info("morning_briefing: cycle started", "force", force)

	// ── Phase A: read watermark ──────────────────────────────────────────────
	meta, err := system.GetBriefingMeta(ctx, app)
	if err != nil {
		return nil, fmt.Errorf("morning briefing: read meta: %w", err)
	}
	log.Info("morning_briefing: watermark read", "last_processed_at", meta.LastProcessedAt)

	// ── Phase B: fetch Gravel (log entries since watermark) ──────────────────
	gravelEntries, err := briefingFetchGravel(ctx, app, meta.LastProcessedAt)
	if err != nil {
		return nil, fmt.Errorf("morning briefing: fetch gravel: %w", err)
	}
	log.Info("morning_briefing: gravel fetched", "count", len(gravelEntries))

	// ── Phase C: fetch Gold (active goals and projects) ──────────────────────
	goldNodes, err := briefingFetchGold(ctx, app)
	if err != nil {
		// Non-fatal: proceed with empty Gold block.
		log.Warn("morning_briefing: failed to fetch gold nodes (continuing without)", "error", err)
		goldNodes = nil
	}
	log.Info("morning_briefing: gold fetched", "count", len(goldNodes))

	now := time.Now()
	dateStr := now.Format("2006-01-02")
	chatID, err := parseTelegramChatID(app)
	if err != nil {
		return nil, fmt.Errorf("morning briefing: parse chat id: %w", err)
	}

	// ── Phase D: sparse check — fallback path ────────────────────────────────
	if len(gravelEntries) == 0 && !force {
		log.Info("morning_briefing: no new gravel — sending fallback message")
		msg := briefingFormatFallbackMessage(dateStr, goldNodes)
		if sendErr := telegram.SendMessage(ctx, app.Config(), chatID, msg, log); sendErr != nil {
			return nil, fmt.Errorf("morning briefing: send fallback: %w", sendErr)
		}
		if setErr := briefingUpdateWatermark(ctx, app, meta, now); setErr != nil {
			log.Warn("morning_briefing: failed to update watermark after fallback (non-fatal)", "error", setErr)
		}
		return &MorningBriefingResult{MessageSent: msg}, nil
	}

	// ── Phase E: build prompt and call LLM ───────────────────────────────────
	prompt, err := prompts.BuildMorningBriefing(prompts.MorningBriefingData{
		Today:         dateStr,
		CurrentTime:   now.Format("15:04 MST"),
		GravelEntries: utils.WrapAsUserData(formatEntriesForPrompt(gravelEntries)),
		GoldNodes:     utils.WrapAsUserData(briefingFormatGoldNodes(goldNodes)),
	})
	if err != nil {
		return nil, fmt.Errorf("morning briefing: build prompt: %w", err)
	}

	log.Info("morning_briefing: calling LLM with native CoT")
	session, err := infra.NewChatSession(ctx, app, "", nil, true)
	if err != nil {
		return nil, fmt.Errorf("morning briefing: create chat session: %w", err)
	}
	resp, err := session.SendMessage(ctx, &genai.Part{Text: prompt})
	if err != nil {
		return nil, fmt.Errorf("morning briefing: LLM call: %w", err)
	}

	thinking, raw := infra.ExtractThinkingAndAnswer(resp)
	if thinking != "" {
		log.Debug("morning_briefing: CoT trace", "thinking", thinking)
	}
	raw = strings.TrimSpace(raw)
	log.Debug("morning_briefing: LLM response received", "response", raw)

	// ── Phase F: parse LLM output ────────────────────────────────────────────
	// The LLM may return keys as section headers (multi-line bullet lists) or
	// as inline key: value pairs — check both maps.
	simple, sections := utils.ParseKeyValueMap(raw)
	resolveField := func(key string) string {
		if v := strings.TrimSpace(simple[key]); v != "" {
			return v
		}
		return strings.TrimSpace(strings.Join(sections[key], "\n"))
	}
	patterns := resolveField("patterns")
	alignment := resolveField("alignment")
	coachingQ := resolveField("coaching_question")

	if patterns == "" && alignment == "" && coachingQ == "" {
		return nil, fmt.Errorf("morning briefing: LLM returned no usable output")
	}
	if coachingQ == "" {
		coachingQ = "What's the one thing you most need to move forward today?"
	}

	// ── Phase G: format and push Telegram ────────────────────────────────────
	msg := briefingFormatMessage(dateStr, patterns, alignment, coachingQ)
	if sendErr := telegram.SendMessage(ctx, app.Config(), chatID, msg, log); sendErr != nil {
		// Do not update watermark — retry next cycle.
		return nil, fmt.Errorf("morning briefing: send telegram: %w", sendErr)
	}
	log.Info("morning_briefing: telegram message sent")

	// ── Phase H: update watermark ─────────────────────────────────────────────
	if setErr := briefingUpdateWatermark(ctx, app, meta, now); setErr != nil {
		log.Warn("morning_briefing: failed to update watermark (non-fatal)", "error", setErr)
	}
	log.Info("morning_briefing: cycle complete")

	return &MorningBriefingResult{MessageSent: msg}, nil
}

// briefingFetchGravel returns log entries with timestamp > sinceTimestamp, oldest first,
// capped at briefingMaxEntries. When sinceTimestamp is empty, returns the most recent entries.
func briefingFetchGravel(ctx context.Context, app *infra.App, sinceTimestamp string) ([]memory.Entry, error) {
	if sinceTimestamp == "" {
		return app.MemoryEntries().List(ctx, briefingMaxEntries, false)
	}
	client, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch gravel: firestore: %w", err)
	}
	docs, err := client.Collection(memory.KnowledgeCollection).
		Where("node_type", "==", memory.NodeTypeLog).
		Where("timestamp", ">", sinceTimestamp).
		OrderBy("timestamp", firestore.Asc).
		Limit(briefingMaxEntries).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("fetch gravel: query: %w", err)
	}
	entries := make([]memory.Entry, 0, len(docs))
	for _, doc := range docs {
		var e memory.Entry
		if err := doc.DataTo(&e); err != nil {
			continue
		}
		e.UUID = doc.Ref.ID
		entries = append(entries, e)
	}
	return entries, nil
}

// briefingFetchGold returns active goal and project nodes ordered by significance descending.
func briefingFetchGold(ctx context.Context, app *infra.App) ([]briefingGoldNode, error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch gold: firestore: %w", err)
	}
	activeStatuses := []any{"active", "planning", "blocked"}
	goalDocs, err := client.Collection(memory.KnowledgeCollection).
		Where("node_type", "==", memory.NodeTypeGoal).
		Where("metadata.status", "in", activeStatuses).
		OrderBy("significance_weight", firestore.Desc).
		Limit(briefingMaxGoldNodes).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("fetch gold: goal query: %w", err)
	}
	projectDocs, err := client.Collection(memory.KnowledgeCollection).
		Where("node_type", "==", memory.NodeTypeProject).
		Where("metadata.status", "in", activeStatuses).
		OrderBy("significance_weight", firestore.Desc).
		Limit(briefingMaxGoldNodes).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("fetch gold: project query: %w", err)
	}

	allDocs := append(goalDocs, projectDocs...)
	nodes := make([]briefingGoldNode, 0, len(allDocs))
	for _, doc := range allDocs {
		data := doc.Data()
		content, _ := data["content"].(string)
		nodeType, _ := data["node_type"].(string)
		sig, _ := data["significance_weight"].(float64)
		status := ""
		if meta, ok := data["metadata"].(map[string]any); ok {
			status, _ = meta["status"].(string)
		}
		nodes = append(nodes, briefingGoldNode{
			Content:           content,
			NodeType:          nodeType,
			Status:            status,
			SignificanceWeight: sig,
		})
	}
	// Re-sort combined slice by significance descending and cap.
	for i := 1; i < len(nodes); i++ {
		for j := i; j > 0 && nodes[j].SignificanceWeight > nodes[j-1].SignificanceWeight; j-- {
			nodes[j], nodes[j-1] = nodes[j-1], nodes[j]
		}
	}
	if len(nodes) > briefingMaxGoldNodes {
		nodes = nodes[:briefingMaxGoldNodes]
	}
	return nodes, nil
}

// briefingUpdateWatermark writes the new briefing watermark.
func briefingUpdateWatermark(ctx context.Context, app *infra.App, meta system.BriefingMeta, now time.Time) error {
	return system.SetBriefingMeta(ctx, app, system.BriefingMeta{
		LastProcessedAt: now.Format(time.RFC3339),
		Version:         meta.Version,
	})
}

// parseTelegramChatID parses AllowedTelegramUserID from config as the primary chat ID.
func parseTelegramChatID(app *infra.App) (int64, error) {
	raw := app.Config().AllowedTelegramUserID
	if raw == "" {
		return 0, fmt.Errorf("ALLOWED_TELEGRAM_USER_ID is not configured")
	}
	var id int64
	if _, err := fmt.Sscanf(raw, "%d", &id); err != nil {
		return 0, fmt.Errorf("parse chat id %q: %w", raw, err)
	}
	return id, nil
}


// briefingFormatGoldNodes renders active goals/projects for the LLM prompt.
func briefingFormatGoldNodes(nodes []briefingGoldNode) string {
	if len(nodes) == 0 {
		return "(no active goals or projects)"
	}
	var sb strings.Builder
	for i, n := range nodes {
		sb.WriteString(fmt.Sprintf("%d. [%s/%s] %s\n", i+1, n.NodeType, n.Status, n.Content))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// briefingFormatMessage formats the full Telegram Markdown briefing message.
func briefingFormatMessage(date, patterns, alignment, coachingQuestion string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*Morning Briefing — %s*\n\n", date))
	if patterns != "" {
		sb.WriteString("*Observed Patterns*\n")
		sb.WriteString(patterns)
		sb.WriteString("\n\n")
	}
	if alignment != "" {
		sb.WriteString("*Project Alignment*\n")
		sb.WriteString(alignment)
		sb.WriteString("\n\n")
	}
	sb.WriteString("*Coaching Question*\n")
	sb.WriteString(fmt.Sprintf("_%s_", coachingQuestion))
	return sb.String()
}

// briefingFormatFallbackMessage formats the minimal fallback Telegram message
// sent when no new Gravel entries exist since the last briefing.
func briefingFormatFallbackMessage(date string, nodes []briefingGoldNode) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*Morning Briefing — %s*\n\n", date))
	sb.WriteString("Nothing new since your last entry. Your active focus:\n")
	top := nodes
	if len(top) > briefingFallbackTopN {
		top = top[:briefingFallbackTopN]
	}
	for _, n := range top {
		sb.WriteString(fmt.Sprintf("• %s\n", n.Content))
	}
	return strings.TrimRight(sb.String(), "\n")
}
