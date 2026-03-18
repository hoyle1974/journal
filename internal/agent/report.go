package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/genai"
)

// maxFilteredLogLines is the maximum number of decision-point log lines passed to the report LLM.
const maxFilteredLogLines = 50

// decisionMarkers are substrings used to identify decision-point log lines worth including in the report.
var decisionMarkers = []string{
	"foh: iteration decision",
	"foh: tool call",
	"foh: query completed",
	"foh: query started",
	"knowledge gap",
	"loop detected",
	"backoff",
	"forced conclusion",
}

// GenerateDebugReport generates a first-person narrative report of what the agent did during a query.
// It uses the structured toolCalls and filtered decision-point debugLogs collected by the FOH loop.
// On failure it returns an empty string so callers can degrade gracefully.
func GenerateDebugReport(ctx context.Context, app infra.ToolEnv, question string, toolCalls []map[string]interface{}, debugLogs []string, answer string) string {
	ctx, span := infra.StartSpan(ctx, "agent.debug_report")
	defer span.End()

	prompt, err := prompts.BuildDebugReport(prompts.DebugReportData{
		Question:         question,
		ToolCallsSummary: buildToolCallsSummary(toolCalls),
		FilteredLogs:     filterDecisionLogs(debugLogs),
		Answer:           utils.TruncateString(answer, 120),
	})
	if err != nil {
		infra.LoggerFrom(ctx).Error("debug report: prompt build failed", "error", err)
		return ""
	}

	resp, err := app.Dispatch(ctx, &infra.LLMRequest{
		Parts:     []*genai.Part{{Text: utils.WrapAsUserData(prompt)}},
		GenConfig: &infra.GenConfig{MaxOutputTokens: 512},
	})
	if err != nil {
		infra.LoggerFrom(ctx).Error("debug report: LLM call failed", "error", err)
		return ""
	}

	narrative := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	infra.LoggerFrom(ctx).Debug("debug report generated", "length", len(narrative))
	return narrative
}

// buildToolCallsSummary renders the structured ToolCalls slice into a concise numbered list.
func buildToolCallsSummary(toolCalls []map[string]interface{}) string {
	if len(toolCalls) == 0 {
		return "(no tools called)"
	}
	var sb strings.Builder
	for i, tc := range toolCalls {
		name, _ := tc["tool"].(string)
		success, _ := tc["success"].(bool)
		preview, _ := tc["result_preview"].(string)

		argsStr := ""
		if args, ok := tc["arguments"]; ok {
			if b, err := json.Marshal(args); err == nil {
				argsStr = utils.TruncateString(string(b), 200)
			}
		}
		preview = utils.TruncateString(preview, 150)
		sb.WriteString(fmt.Sprintf("%d. %s(args=%s) → success=%v result=%q\n",
			i+1, name, argsStr, success, preview))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// GenerateProcessEntryReport generates a first-person narrative of what happened during process-entry.
// On failure it returns an empty string so callers can degrade gracefully.
func GenerateProcessEntryReport(ctx context.Context, app infra.ToolEnv, r *ProcessEntryReport) string {
	ctx, span := infra.StartSpan(ctx, "agent.process_entry_report")
	defer span.End()

	if r == nil {
		return ""
	}
	prompt, err := prompts.BuildProcessEntryReport(prompts.ProcessEntryReportData{
		Content:        r.Content,
		Source:         r.Source,
		Significance:   r.Significance,
		Domain:         r.Domain,
		FactStored:     r.FactStored,
		TaskCreated:    r.TaskCreated,
		ContextsLinked: r.ContextsLinked,
		Mood:           r.Mood,
		Tags:           r.Tags,
		EntityNames:    r.EntityNames,
	})
	if err != nil {
		infra.LoggerFrom(ctx).Error("process entry report: prompt build failed", "error", err)
		return ""
	}

	resp, err := app.Dispatch(ctx, &infra.LLMRequest{
		Parts:     []*genai.Part{{Text: utils.WrapAsUserData(prompt)}},
		GenConfig: &infra.GenConfig{MaxOutputTokens: 400},
	})
	if err != nil {
		infra.LoggerFrom(ctx).Error("process entry report: LLM call failed", "error", err)
		return ""
	}

	narrative := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	infra.LoggerFrom(ctx).Debug("process entry report generated", "length", len(narrative))
	return narrative
}

// DreamerReportInput holds the full content collected during a dream run for the process narrative.
type DreamerReportInput struct {
	EntriesProcessed    int
	FactsExtracted      int
	FactsWritten        int
	ContextsSynthesized int
	PersonaFacts        []string
	EvolutionAudit      *EvolutionAuditOutput
	RoomTranscript      string           // colloquium discussion between specialist agents
	DomainOutputs       []*SpecialistOutput // per-domain extracted facts and summaries
	MergedFacts         []string         // content of facts written to memory after dedup
}

const (
	dreamerReportRoomTranscriptMax = 4000 // chars
	dreamerReportMergedFactsMax    = 40   // entries
)

// GenerateDreamerReport generates a first-person process narrative of what happened during a dream run.
// On failure it returns an empty string so callers can degrade gracefully.
func GenerateDreamerReport(ctx context.Context, app infra.ToolEnv, in *DreamerReportInput) string {
	ctx, span := infra.StartSpan(ctx, "agent.dreamer_report")
	defer span.End()

	if in == nil {
		return ""
	}

	// Build domain facts text: one section per domain showing facts extracted.
	var domainBuf strings.Builder
	for _, out := range in.DomainOutputs {
		if out == nil || len(out.Facts) == 0 {
			continue
		}
		domainBuf.WriteString(fmt.Sprintf("[%s — %d facts]\n", out.Domain, len(out.Facts)))
		for i, f := range out.Facts {
			domainBuf.WriteString(fmt.Sprintf("  %d. %s\n", i+1, f))
		}
	}

	// Build merged facts text (capped to avoid exceeding context).
	var mergedBuf strings.Builder
	cap := in.MergedFacts
	if len(cap) > dreamerReportMergedFactsMax {
		cap = cap[:dreamerReportMergedFactsMax]
	}
	for i, f := range cap {
		mergedBuf.WriteString(fmt.Sprintf("  %d. %s\n", i+1, f))
	}
	if len(in.MergedFacts) > dreamerReportMergedFactsMax {
		mergedBuf.WriteString(fmt.Sprintf("  ... (%d more)\n", len(in.MergedFacts)-dreamerReportMergedFactsMax))
	}

	// Truncate room transcript if necessary.
	roomText := in.RoomTranscript
	if len(roomText) > dreamerReportRoomTranscriptMax {
		roomText = roomText[:dreamerReportRoomTranscriptMax] + "\n... (truncated)"
	}

	data := prompts.DreamerReportData{
		EntriesProcessed:    in.EntriesProcessed,
		FactsExtracted:      in.FactsExtracted,
		FactsWritten:        in.FactsWritten,
		ContextsSynthesized: in.ContextsSynthesized,
		PersonaFacts:        in.PersonaFacts,
		RoomTranscriptText:  roomText,
		DomainFactsText:     domainBuf.String(),
		MergedFactsText:     mergedBuf.String(),
	}
	if in.EvolutionAudit != nil {
		data.EvolutionSummary = in.EvolutionAudit.Summary
		data.EvolutionOpenLoops = in.EvolutionAudit.Facts
		data.EvolutionDevRequests = in.EvolutionAudit.Entities
	}

	prompt, err := prompts.BuildDreamerReport(data)
	if err != nil {
		infra.LoggerFrom(ctx).Error("dreamer report: prompt build failed", "error", err)
		return ""
	}

	resp, err := app.Dispatch(ctx, &infra.LLMRequest{
		Parts:     []*genai.Part{{Text: utils.WrapAsUserData(prompt)}},
		GenConfig: &infra.GenConfig{MaxOutputTokens: 800},
	})
	if err != nil {
		infra.LoggerFrom(ctx).Error("dreamer report: LLM call failed", "error", err)
		return ""
	}

	narrative := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	infra.LoggerFrom(ctx).Debug("dreamer report generated", "length", len(narrative))
	return narrative
}

// filterDecisionLogs keeps only lines containing a decision-point marker, capped at maxFilteredLogLines.
func filterDecisionLogs(logs []string) string {
	if len(logs) == 0 {
		return "(no decision logs)"
	}
	var filtered []string
	for _, line := range logs {
		lc := strings.ToLower(line)
		for _, marker := range decisionMarkers {
			if strings.Contains(lc, marker) {
				filtered = append(filtered, line)
				break
			}
		}
	}
	if len(filtered) == 0 {
		return "(no decision-point logs matched)"
	}
	// Keep the most recent lines if over the cap.
	if len(filtered) > maxFilteredLogLines {
		filtered = filtered[len(filtered)-maxFilteredLogLines:]
	}
	return strings.Join(filtered, "\n")
}
