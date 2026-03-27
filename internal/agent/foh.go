package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
	"google.golang.org/genai"
)

const (
	MaxIterations       = 10
	MaxMessagePairs     = 20
	ToolRepeatBackOffAt = 3
)

// FOHEnv is the interface the FOH (query agent) needs: ToolEnv plus context attachment, tool pool, and access to *App for entry/save-query.
// Implemented by *infra.App. Pass explicitly so the agent does not depend on the concrete type.
type FOHEnv interface {
	infra.ToolEnv
	WithContext(ctx context.Context) context.Context
	SubmitToToolPool(task func()) error
	App() *infra.App
}

type entryUUIDKey struct{}
type entryAlreadyAddedKey struct{}

// WithCurrentEntryUUID returns a context that carries the current journal entry UUID (e.g. the query that triggered FOH).
func WithCurrentEntryUUID(ctx context.Context, entryUUID string) context.Context {
	return context.WithValue(ctx, entryUUIDKey{}, entryUUID)
}

// CurrentEntryUUIDFrom returns the current entry UUID from context, or "" if not set.
func CurrentEntryUUIDFrom(ctx context.Context) string {
	if s, ok := ctx.Value(entryUUIDKey{}).(string); ok && s != "" {
		return s
	}
	return ""
}

// WithEntryAlreadyAdded marks that the caller already created a journal entry for this query (e.g. Telegram image).
// FOH will skip AddEntryAndEnqueue and use this UUID as the current entry.
func WithEntryAlreadyAdded(ctx context.Context, entryUUID string) context.Context {
	return context.WithValue(ctx, entryAlreadyAddedKey{}, entryUUID)
}

// EntryAlreadyAddedUUID returns the entry UUID if the caller already added an entry, or "" if not set.
func EntryAlreadyAddedUUID(ctx context.Context) string {
	if s, ok := ctx.Value(entryAlreadyAddedKey{}).(string); ok && s != "" {
		return s
	}
	return ""
}

func withCurrentEntryUUID(ctx context.Context, entryUUID string) context.Context {
	return WithCurrentEntryUUID(ctx, entryUUID)
}

// QueryResult represents the result of a query.
type QueryResult struct {
	Answer           string                   `json:"answer"`
	Iterations       int                      `json:"iterations"`
	ToolCalls        []map[string]interface{} `json:"tool_calls,omitempty"`
	ForcedConclusion bool                     `json:"forced_conclusion,omitempty"`
	Error            bool                     `json:"error"`
	DebugLogs        []string                 `json:"debug_logs,omitempty"`
	// DebugTrace holds the full chronological trace: system prompt, reasoning blocks,
	// tool calls, and tool results. Each entry is prefixed with its type (e.g. "Prompt: ", "Reasoning: ", "Tool[N]: ", "Result[N]: ").
	DebugTrace []string `json:"debug_trace,omitempty"`
}

// ErrQueryResult returns a failed QueryResult.
func ErrQueryResult(answer string, iteration int, debugLogs []string, debugTrace []string) *QueryResult {
	return &QueryResult{Answer: answer, Iterations: iteration, Error: true, DebugLogs: debugLogs, DebugTrace: debugTrace}
}

// RunQuery runs a query against the journal using the agentic loop.
func RunQuery(ctx context.Context, app FOHEnv, question, source string) *QueryResult {
	return RunQueryWithDebug(ctx, app, question, source, true)
}

// RunQueryWithDebug is preserved for backward compat; passes empty ragContext.
func RunQueryWithDebug(ctx context.Context, app FOHEnv, question, source string, debug bool) *QueryResult {
	return RunQueryFull(ctx, app, question, source, debug, "")
}

// RunQueryFull is the unified pipeline entry point.
// ragContext is injected into the system prompt as GRAPH CONTEXT; pass "" to skip.
func RunQueryFull(ctx context.Context, app FOHEnv, question, source string, debug bool, ragContext string) *QueryResult {
	ctx = app.WithContext(ctx)
	var debugLogs []string
	logDebug := func(msg string, args ...interface{}) {
		if debug {
			line := fmt.Sprintf(msg, args...)
			debugLogs = append(debugLogs, line)
			// Mirror to server (no truncation) so a log session can be turned into a story with queryable details.
			infra.LoggerFrom(ctx).Debug(line)
		}
	}
	queryRunID := infra.GenShortRunID()
	startTime := time.Now()
	infra.LoggerFrom(ctx).Debug("FOH: query started", "query_run_id", queryRunID, "phase", "start", "question", question, "source", source)

	if app == nil {
		return ErrQueryResult("Error: no app in context (GEMINI_API_KEY not configured?)", 0, nil, nil)
	}

	var entryUUID string
	if existing := EntryAlreadyAddedUUID(ctx); existing != "" {
		entryUUID = existing
		ctx = withCurrentEntryUUID(ctx, entryUUID)
		infra.LoggerFrom(ctx).Debug("FOH: using caller-provided entry (skip log)", "query_run_id", queryRunID, "phase", "start", "event", "query_start", "question", question, "entry_uuid", entryUUID, "source", source)
	} else {
	
		var err error
		entryUUID, err = AddEntryAndEnqueue(ctx, app.App(), question, source, nil, "")
		if err != nil {
			infra.LoggerFrom(ctx).Error("failed to log user input", "error", err)
			return ErrQueryResult(fmt.Sprintf("Error saving input: %v", err), 0, debugLogs, nil)
		}
		ctx = withCurrentEntryUUID(ctx, entryUUID)
		infra.LoggerFrom(ctx).Debug("FOH: user input logged as entry", "query_run_id", queryRunID, "phase", "start", "event", "query_start", "question", question, "entry_uuid", entryUUID, "source", source)
	}

	logDebug("[start] Question: %s", question)
	var debugTrace []string

	systemPrompt, err := BuildSystemPrompt(ctx, app, ragContext)
	if err != nil {
		return ErrQueryResult(fmt.Sprintf("Error building system prompt: %v", err), 0, debugLogs, nil)
	}
	debugTrace = append(debugTrace, "Prompt: "+systemPrompt)
	infra.LoggerFrom(ctx).Debug("FOH: system prompt built", "query_run_id", queryRunID, "phase", "start", "prompt_len", len(systemPrompt), "reason", "inject date, recent history, tasks, project")
	logDebug("[prompt] %s", systemPrompt)

	toolDefs := tools.GetDefinitions()
	session, err := infra.NewChatSession(ctx, app.App(), systemPrompt, toolDefs, true)
	if err != nil {
		return ErrQueryResult(fmt.Sprintf("Error creating chat session: %v", infra.WrapLLMError(err)), 0, debugLogs, nil)
	}
	logDebug("[init] Chat session created with %d tools", len(toolDefs))
	infra.LoggerFrom(ctx).Debug("FOH: sending question to LLM", "query_run_id", queryRunID, "phase", "first_turn", "tool_count", len(toolDefs), "reason", "first turn")

	iteration := 0
	emptyContentRetries := 0
	var toolCalls []map[string]interface{}
	var lastToolCallSignature string
	toolCallCounts := make(map[string]int)
	var repeatedToolName string
	var knowledgeGapDetected bool
	var retrievedContent strings.Builder
	searchToolCallCount := 0

	resp, err := session.SendMessage(ctx, &genai.Part{Text: question})
	if err != nil {
		return ErrQueryResult(fmt.Sprintf("Error calling Gemini API: %v", infra.WrapLLMError(err)), 1, debugLogs, nil)
	}
	iteration++

	logDebug("[iter %d] Sent question to LLM", iteration)
	infra.LoggerFrom(ctx).Debug("FOH: iteration 1 response received", "query_run_id", queryRunID, "phase", "first_turn", "llm_correlation_id", session.LastLLMCorrelationID(), "reason", "initial LLM turn")

	for iteration < MaxIterations {
		thinking, answerText := infra.ExtractThinkingAndAnswer(resp)
		if thinking != "" {
			if thoughtSuggestsKnowledgeGap(thinking) {
				knowledgeGapDetected = true
			}
			debugTrace = append(debugTrace, fmt.Sprintf("Reasoning[%d]: %s", iteration, strings.TrimSpace(thinking)))
			infra.LoggerFrom(ctx).Debug("FOH: thinking block", "query_run_id", queryRunID, "iter", iteration, "thinking_len", len(thinking))
			if debug {
				logDebug("[iter %d] thinking: %s", iteration, thinking)
			}
		}
		hasCalls := infra.HasFunctionCalls(resp)

		logDebug("[iter %d] LLM response: has_function_calls=%v", iteration, hasCalls)
		infra.LoggerFrom(ctx).Debug("FOH: iteration decision", "query_run_id", queryRunID, "phase", "decision", "iter", iteration, "has_function_calls", hasCalls, "llm_correlation_id", session.LastLLMCorrelationID(), "reason", "decompose: LLM either answers or calls tools")

		if !hasCalls {
			if answerText != "" {
				answer, missingFromAudit := extractMissingInfoAndAnswer(answerText)
				if len(missingFromAudit) > 0 {
					knowledgeGapDetected = true
					infra.LoggerFrom(ctx).Debug("FOH: model reported missing info (unified audit)", "query_run_id", queryRunID, "phase", "answer", "missing", missingFromAudit)
					logDebug("[audit] missing_info: %v", missingFromAudit)
				}

				toolNamesFromCalls := make([]string, 0, len(toolCalls))
				for _, tc := range toolCalls {
					if t, ok := tc["tool"].(string); ok {
						toolNamesFromCalls = append(toolNamesFromCalls, t)
					}
				}
				infra.LogAssistantEfficiency(ctx, len(systemPrompt)+len(question), len(answer), iteration)
				infra.LoggerFrom(ctx).Debug("FOH: final answer", "query_run_id", queryRunID, "phase", "answer", "event", "query_complete", "question", question, "answer", answer, "iterations", iteration, "tool_call_count", len(toolCalls), "tool_names", strings.Join(toolNamesFromCalls, ","), "duration_ms", time.Since(startTime).Milliseconds())
				infra.LoggerFrom(ctx).Info("query completed",
					"query_run_id", queryRunID,
					"phase", "answer",
					"iterations", iteration,
					"tool_calls", len(toolCalls),
					"duration_ms", time.Since(startTime).Milliseconds(),
				)

				if !strings.HasPrefix(answer, "Error:") {
					if err := EnqueueSaveQuery(ctx, app.App(), question, answer, source, knowledgeGapDetected); err != nil {
						infra.LoggerFrom(ctx).Warn("failed to enqueue save-query task", "error", err)
					}
				}

				logDebug("[DEBUG] LLM Final Response: %q (%d chars) after %d iterations", answer, len(answer), iteration)
				return &QueryResult{
					Answer:         answer,
					Iterations:     iteration,
					ToolCalls:      toolCalls,
					Error:          false,
					DebugLogs:      debugLogs,
					DebugTrace: debugTrace,
				}
			}
		}

		functionCalls := infra.ExtractFunctionCalls(resp)
		if len(functionCalls) == 0 {
			const maxEmptyRetries = 2
			if emptyContentRetries < maxEmptyRetries {
				emptyContentRetries++
				delay := time.Duration(emptyContentRetries) * time.Second
				logDebug("[retry] No text or function calls (attempt %d/%d), waiting %v then retrying", emptyContentRetries, maxEmptyRetries, delay)
				time.Sleep(delay)
				resp2, err2 := session.SendMessage(ctx, &genai.Part{Text: question})
				if err2 != nil {
				
					return &QueryResult{
						Answer:         fmt.Sprintf("Error calling Gemini API: %v", infra.WrapLLMError(err2)),
						Iterations:     iteration,
						Error:          true,
						DebugLogs:      debugLogs,
					}
				}
			
				if infra.ExtractTextFromResponse(resp2) != "" || infra.HasFunctionCalls(resp2) {
					resp = resp2
					logDebug("[retry] Retry had content, continuing")
					continue
				}
				resp = resp2
			}
			if iteration > 1 {
				logDebug("[done] LLM returned empty content after tool execution, defaulting to short summary.")
				return &QueryResult{
					Answer:         "Saved.",
					Iterations:     iteration,
					ToolCalls:      toolCalls,
					Error:          false,
					DebugLogs:      debugLogs,
					DebugTrace: debugTrace,
				}
			}

			reason := infra.EmptyResponseReason(resp)
		
			logDebug("[error] No text or function calls in response: %s", reason)
			msg := "The model returned no content. This can happen occasionally; please try again."
			if !strings.Contains(strings.ToLower(reason), "stop") {
				msg = fmt.Sprintf("Error: The model returned no content (%s). Please try again.", reason)
			}
			return &QueryResult{
				Answer:         msg,
				Iterations:     iteration,
				Error:          true,
				DebugLogs:      debugLogs,
			}
		}

		toolNames := make([]string, 0, len(functionCalls))
		var sigParts []string
		for _, fc := range functionCalls {
			toolNames = append(toolNames, fc.Name)
			argsJSON, _ := json.Marshal(fc.Args)
			sigParts = append(sigParts, fmt.Sprintf("%s:%s", fc.Name, string(argsJSON)))
			debugTrace = append(debugTrace, fmt.Sprintf("Tool[%d]: %s(%s)", iteration, fc.Name, string(argsJSON)))
			logDebug("[iter %d] tool_call: %s(%s)", iteration, fc.Name, string(argsJSON))
			infra.LoggerFrom(ctx).Debug("FOH: tool call", "query_run_id", queryRunID, "phase", "tool_execution", "event", "tool_call", "iter", iteration, "tool", fc.Name, "args", string(argsJSON))
		}
		infra.LoggerFrom(ctx).Debug("FOH: executing tools", "query_run_id", queryRunID, "phase", "tool_execution", "iter", iteration, "tools", toolNames, "reason", "execute: run tools in parallel then send results back to LLM")
		currentSignature := strings.Join(sigParts, "|")

		if currentSignature == lastToolCallSignature && lastToolCallSignature != "" {
			infra.LoggerFrom(ctx).Debug("FOH: breaking loop", "query_run_id", queryRunID, "phase", "tool_execution", "reason", "same tool call signature repeated; forcing conclusion")
			logDebug("[warning] Detected tool call loop, forcing conclusion")
			infra.LoggerFrom(ctx).Warn("detected tool call loop", "query_run_id", queryRunID, "phase", "tool_execution", "signature", utils.TruncateString(currentSignature, 100))
			break
		}
		lastToolCallSignature = currentSignature

		repeatedToolName = ""
		for _, fc := range functionCalls {
			key := fc.Name + ":" + normalizedToolArgs(fc.Args)
			toolCallCounts[key]++
			if toolCallCounts[key] >= ToolRepeatBackOffAt {
				repeatedToolName = fc.Name
				logDebug("[backoff] Tool %q called %d times with similar args", fc.Name, toolCallCounts[key])
				infra.LoggerFrom(ctx).Warn("tool repeat back-off", "query_run_id", queryRunID, "phase", "tool_execution", "tool", fc.Name, "count", toolCallCounts[key])
				break
			}
		}

		type toolExecResult struct {
			index  int
			fcName string
			args   map[string]interface{}
			result tools.Result
		}

		results := make([]toolExecResult, len(functionCalls))
		var wg sync.WaitGroup
		var mu sync.Mutex

		for i, fc := range functionCalls {
			wg.Add(1)
			idx := i
			fcName := fc.Name
			args := make(map[string]interface{})
			for k, v := range fc.Args {
				args[k] = v
			}

			execFunc := func() {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						infra.LoggerFrom(ctx).Error("tool panicked", "tool", fcName, "panic", r)
						mu.Lock()
						results[idx] = toolExecResult{
							index:  idx,
							fcName: fcName,
							args:   args,
							result: tools.Fail("Tool encountered a fatal error: %v", r),
						}
						mu.Unlock()
					}
				}()

				toolResult := tools.Execute(ctx, app, fcName, args)
				mu.Lock()
				results[idx] = toolExecResult{index: idx, fcName: fcName, args: args, result: toolResult}
				mu.Unlock()

				infra.LoggerFrom(ctx).Debug("tool executed", "query_run_id", queryRunID, "phase", "tool_execution", "event", "tool_result", "iter", iteration, "tool", fcName, "success", toolResult.Success, "result", toolResult.Result)
				resultPreview := toolResult.Result
				if len(resultPreview) > 500 {
					resultPreview = resultPreview[:500] + "..."
				}
				mu.Lock()
				logDebug("[iter %d] tool_result: %s -> %s", iteration, fcName, resultPreview)
				mu.Unlock()
			}

			if app != nil {
				if err := app.SubmitToToolPool(execFunc); err != nil {
					wg.Done()
					wg.Add(1)
					execFunc()
				}
			} else {
				execFunc()
			}
		}
		wg.Wait()

		var functionResponses []*genai.Part
		searchToolCalled := false
		searchTools := map[string]bool{
			"semantic_search": true, "get_entity_network": true, "search_entries": true,
			"get_entries_by_date_range": true, "query_entities": true, "wikipedia": true,
			"web_search": true, "list_knowledge": true,
			"graph_expand": true,
		}
		for _, r := range results {
			toolCalls = append(toolCalls, map[string]interface{}{
				"tool":           r.fcName,
				"arguments":      r.args,
				"success":        r.result.Success,
				"result_preview": utils.TruncateString(r.result.Result, 200),
			})
			debugTrace = append(debugTrace, fmt.Sprintf("Result[%d]: %s -> %s", iteration, r.fcName, r.result.Result))

			functionResponses = append(functionResponses, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     r.fcName,
					Response: map[string]any{"result": utils.SanitizePrompt(r.result.Result)},
				},
			})

			if searchTools[r.fcName] {
				searchToolCalled = true
				searchToolCallCount++
				retrievedContent.WriteString(r.result.Result)
				retrievedContent.WriteString("\n\n")
			}
		}

		if searchToolCalled {
			for _, r := range results {
				if !searchTools[r.fcName] {
					continue
				}
				res := strings.ToLower(strings.TrimSpace(r.result.Result))
				if strings.Contains(res, "no results found") || strings.Contains(res, "no information found") ||
					strings.Contains(res, "no semantic matches found") || strings.Contains(res, "no entries found") ||
					strings.Contains(res, "no queries found") || strings.Contains(res, "no entity found") ||
					strings.Contains(res, "no wikipedia") || strings.Contains(res, "no definition found for") {
					knowledgeGapDetected = true
					break
				}
			}
		}

		messageParts := make([]*genai.Part, 0, len(functionResponses)+1)
		for _, p := range functionResponses {
			messageParts = append(messageParts, p)
		}
		if repeatedToolName != "" {
			messageParts = append(messageParts, &genai.Part{Text: buildToolRepeatBackOffPrompt(repeatedToolName)})
			for k := range toolCallCounts {
				if strings.HasPrefix(k, repeatedToolName+":") {
					delete(toolCallCounts, k)
				}
			}
		}

		resp, err = session.SendMessage(ctx, messageParts...)
		if err != nil {
			return &QueryResult{
				Answer:         fmt.Sprintf("Error calling Gemini API: %v", infra.WrapLLMError(err)),
				Iterations:     iteration,
				ToolCalls:      toolCalls,
				Error:          true,
				DebugLogs:      debugLogs,
			}
		}
		iteration++
	
		session.TrimHistory(MaxMessagePairs)
		infra.LoggerFrom(ctx).Debug("FOH: tool results sent to LLM", "query_run_id", queryRunID, "phase", "tool_execution", "iter", iteration, "next_llm_correlation_id", session.LastLLMCorrelationID(), "reason", "reflect: next turn may answer or call more tools")
	}

	logDebug("[warning] Reached max iterations (%d), forcing conclusion", MaxIterations)
	infra.LoggerFrom(ctx).Debug("FOH: forcing conclusion", "query_run_id", queryRunID, "phase", "forced_conclusion", "iterations", MaxIterations, "reason", "max iterations reached; asking LLM for best answer so far")
	infra.LoggerFrom(ctx).Warn("query reached max iterations", "query_run_id", queryRunID, "phase", "forced_conclusion", "max", MaxIterations)

	resp, err = session.SendMessage(ctx, &genai.Part{Text: "Please provide your best answer based on the information gathered so far."})
	if err != nil {
		return &QueryResult{
			Answer:         fmt.Sprintf("Error calling Gemini API: %v", infra.WrapLLMError(err)),
			Iterations:     iteration,
			ToolCalls:      toolCalls,
			Error:          true,
			DebugLogs:      debugLogs,
		}
	}

	thinkingForced, strippedForced := infra.ExtractThinkingAndAnswer(resp)
	if thinkingForced != "" {
		if thoughtSuggestsKnowledgeGap(thinkingForced) {
			knowledgeGapDetected = true
		}
		debugTrace = append(debugTrace, fmt.Sprintf("Reasoning[%d]: %s", iteration, strings.TrimSpace(thinkingForced)))
		infra.LoggerFrom(ctx).Debug("FOH: thinking block (forced conclusion)", "query_run_id", queryRunID, "phase", "forced_conclusion", "thinking_len", len(thinkingForced))
		if debug {
			logDebug("[forced] thinking: %s", thinkingForced)
		}
	}
	if strippedForced != "" {
		answer, missingFromAudit := extractMissingInfoAndAnswer(strippedForced)
		if len(missingFromAudit) > 0 {
			knowledgeGapDetected = true
			infra.LoggerFrom(ctx).Debug("FOH: model reported missing info (forced conclusion)", "query_run_id", queryRunID, "phase", "forced_conclusion", "missing", missingFromAudit)
		}
		if !strings.HasPrefix(answer, "Error:") {
			_ = EnqueueSaveQuery(ctx, app.App(), question, answer, source, knowledgeGapDetected)
		}
		forcedToolNames := make([]string, 0, len(toolCalls))
		for _, tc := range toolCalls {
			if t, ok := tc["tool"].(string); ok {
				forcedToolNames = append(forcedToolNames, t)
			}
		}
		infra.LogAssistantEfficiency(ctx, len(systemPrompt)+len(question), len(answer), iteration)
		infra.LoggerFrom(ctx).Debug("FOH: forced conclusion", "query_run_id", queryRunID, "phase", "forced_conclusion", "event", "query_complete", "question", question, "answer", answer, "iterations", iteration, "tool_call_count", len(toolCalls), "tool_names", strings.Join(forcedToolNames, ","), "duration_ms", time.Since(startTime).Milliseconds(), "forced_conclusion", true)
		logDebug("[done] Forced conclusion after %d iterations", iteration)
		return &QueryResult{
			Answer:           answer,
			Iterations:       iteration,
			ToolCalls:        toolCalls,
			ForcedConclusion: true,
			Error:            false,
			DebugLogs:        debugLogs,
		}
	}

	logDebug("[error] Unable to complete within iteration limits")

	return &QueryResult{
		Answer:         "Error: Unable to complete query within iteration limits.",
		Iterations:     iteration,
		ToolCalls:      toolCalls,
		Error:          true,
		DebugLogs:      debugLogs,
	}
}

// extractMissingInfoAndAnswer parses the model's final answer text. If the model included
// "MISSING_INFO: <semicolon-separated list>" (per unified-audit instructions), that line is
// stripped and the list is returned; otherwise missing is nil. The cleaned answer is returned.
func extractMissingInfoAndAnswer(raw string) (answer string, missing []string) {
	raw = strings.TrimSpace(raw)
	simple, _ := utils.ParseKeyValueMap(raw)
	missingVal := strings.TrimSpace(simple["missing_info"])
	if missingVal == "" {
		return raw, nil
	}
	for _, s := range strings.Split(missingVal, ";") {
		if t := strings.TrimSpace(s); t != "" {
			missing = append(missing, t)
		}
	}
	// Strip the MISSING_INFO line from the answer so it is not shown to the user.
	const prefix = "MISSING_INFO:"
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(trimmed), prefix) {
			continue
		}
		out = append(out, line)
	}
	answer = strings.TrimSpace(strings.Join(out, "\n"))
	if answer == "" {
		answer = raw // fallback: strip failed or left nothing
	}
	return answer, missing
}

func normalizedToolArgs(args map[string]interface{}) string {
	queryKeys := []string{"query", "topic", "q", "search", "entity_name"}
	for _, k := range queryKeys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok {
				return strings.ToLower(strings.TrimSpace(s))
			}
		}
	}
	b, _ := json.Marshal(args)
	return string(b)
}

func buildToolRepeatBackOffPrompt(toolName string) string {
	switch toolName {
	case "wikipedia":
		return "You have already called wikipedia several times with similar queries. Try a different approach: use web_search for current or general information, or summarize what you have found so far and give your best answer."
	case "semantic_search", "search_entries", "get_entries_by_date_range", "query_entities", "get_oldest_entries", "get_entity_network":
		return "You have already called " + toolName + " several times with similar arguments. Either use a different tool (e.g. web_search for external facts), or synthesize what you have and provide your best answer based on the information gathered."
	case "web_search":
		return "You have already called web_search several times. Summarize the results you have and provide your best answer; avoid repeating the same search."
	default:
		return "You have already called " + toolName + " several times with similar arguments. Try a different approach or provide your best answer based on the information you have gathered so far."
	}
}
