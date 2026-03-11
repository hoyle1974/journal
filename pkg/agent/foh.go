package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
)

const (
	MaxIterations       = 10
	MaxMessagePairs     = 20
	ToolRepeatBackOffAt = 3
)

type entryUUIDKey struct{}

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
}

// RunQuery runs a query against the journal using the agentic loop.
func RunQuery(ctx context.Context, app *infra.App, question, source string) *QueryResult {
	return RunQueryWithDebug(ctx, app, question, source, true)
}

// RunQueryWithDebug runs a query with optional debug logging.
func RunQueryWithDebug(ctx context.Context, app *infra.App, question, source string, debug bool) *QueryResult {
	ctx = infra.WithApp(ctx, app)
	var debugLogs []string
	logDebug := func(msg string, args ...interface{}) {
		if debug {
			line := fmt.Sprintf(msg, args...)
			debugLogs = append(debugLogs, line)
			// Mirror to server (no truncation) so a log session can be turned into a story with queryable details.
			infra.LoggerFrom(ctx).Debug(line)
		}
	}
	ctx, span := infra.StartSpan(ctx, "query.run")
	defer span.End()

	queryRunID := infra.GenShortRunID()
	startTime := time.Now()
	infra.QueriesTotal.Inc()
	infra.LoggerFrom(ctx).Debug("FOH: query started", "query_run_id", queryRunID, "phase", "start", "question", question, "source", source)

	span.SetAttributes(map[string]string{
		"question_len": fmt.Sprintf("%d", len(question)),
		"source":       source,
	})

	if app == nil {
		infra.ErrorsTotal.Inc()
		return &QueryResult{
			Answer:     "Error: no app in context (GEMINI_API_KEY not configured?)",
			Iterations: 0,
			Error:      true,
		}
	}

	infra.EntriesTotal.Inc()
	entryUUID, err := AddEntryAndEnqueue(ctx, question, source, nil)
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to log user input", "error", err)
		infra.ErrorsTotal.Inc()
		span.RecordError(err)
		return &QueryResult{
			Answer:     fmt.Sprintf("Error saving input: %v", err),
			Iterations: 0,
			Error:      true,
			DebugLogs:  debugLogs,
		}
	}
	ctx = withCurrentEntryUUID(ctx, entryUUID)
	infra.LoggerFrom(ctx).Debug("FOH: user input logged as entry", "query_run_id", queryRunID, "phase", "start", "event", "query_start", "question", question, "entry_uuid", entryUUID, "source", source)

	logDebug("[start] Question: %s", question)

	systemPrompt := BuildSystemPrompt(ctx)
	infra.LoggerFrom(ctx).Debug("FOH: system prompt built", "query_run_id", queryRunID, "phase", "start", "prompt_len", len(systemPrompt), "reason", "inject date, contexts, recent history")
	logDebug("[prompt] %s", systemPrompt)

	useCompactTools := app.Config() != nil && app.Config().UseCompactTools
	var toolDefs []*genai.FunctionDeclaration
	if useCompactTools {
		toolDefs = tools.GetDefinitionsForCore() // Map vs Manual: only semantic_search, upsert_knowledge, discovery_search (~300 tokens)
	} else {
		toolDefs = tools.GetDefinitions()
	}
	session, err := infra.NewChatSession(ctx, systemPrompt, toolDefs)
	if err != nil {
		infra.ErrorsTotal.Inc()
		span.RecordError(err)
		return &QueryResult{
			Answer:     fmt.Sprintf("Error creating chat session: %v", infra.WrapLLMError(err)),
			Iterations: 0,
			Error:      true,
			DebugLogs:  debugLogs,
		}
	}
	logDebug("[init] Chat session created with %d tools (compact=%v)", len(toolDefs), useCompactTools)
	infra.LoggerFrom(ctx).Debug("FOH: sending question to LLM", "query_run_id", queryRunID, "phase", "first_turn", "tool_count", len(toolDefs), "compact_tools", useCompactTools, "reason", "first turn")

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
		infra.ErrorsTotal.Inc()
		span.RecordError(err)
		return &QueryResult{
			Answer:     fmt.Sprintf("Error calling Gemini API: %v", infra.WrapLLMError(err)),
			Iterations: 1,
			Error:      true,
			DebugLogs:  debugLogs,
		}
	}
	iteration++
	infra.GeminiCallsTotal.Inc()
	logDebug("[iter %d] Sent question to LLM", iteration)
	infra.LoggerFrom(ctx).Debug("FOH: iteration 1 response received", "query_run_id", queryRunID, "phase", "first_turn", "llm_correlation_id", session.LastLLMCorrelationID(), "reason", "initial LLM turn")

	for iteration < MaxIterations {
		var hasCalls bool
		var answerText string
		if useCompactTools {
			// Map vs Manual: core tools (semantic_search, upsert_knowledge, discovery_search) come as native function calls; rest via JSON.
			var discoveredToolName string
			var discoveredToolArgs map[string]interface{}
			if infra.HasFunctionCalls(resp) {
				hasCalls = true
			} else {
				answerText = infra.ExtractTextFromResponse(resp)
				discoveredToolName, discoveredToolArgs, hasCalls = ParseStructuredToolCall(answerText)
			}
			if hasCalls && !infra.HasFunctionCalls(resp) {
				// Discovered tool invoked via JSON block
				logDebug("[iter %d] LLM response: discovered tool_call=%s", iteration, discoveredToolName)
				infra.LoggerFrom(ctx).Debug("FOH: discovered tool call (JSON)", "query_run_id", queryRunID, "phase", "tool_execution", "iter", iteration, "tool", discoveredToolName)
				infra.ToolCallsTotal.Inc()
				toolResult := tools.Execute(ctx, discoveredToolName, discoveredToolArgs)
				toolCalls = append(toolCalls, map[string]interface{}{
					"tool":           discoveredToolName,
					"arguments":      discoveredToolArgs,
					"success":        toolResult.Success,
					"result_preview": utils.TruncateString(toolResult.Result, 200),
				})
				searchTools := map[string]bool{
					"semantic_search": true, "get_entity_network": true, "search_entries": true,
					"get_entries_by_date_range": true, "query_entities": true, "wikipedia": true,
					"web_search": true, "list_knowledge": true,
					"consult_anthropologist": true, "consult_architect": true, "consult_executive": true, "consult_philosopher": true,
				}
				if searchTools[discoveredToolName] {
					searchToolCallCount++
					retrievedContent.WriteString(toolResult.Result)
					retrievedContent.WriteString("\n\n")
					res := strings.ToLower(strings.TrimSpace(toolResult.Result))
					if strings.Contains(res, "no results found") || strings.Contains(res, "no information found") ||
						strings.Contains(res, "no semantic matches found") || strings.Contains(res, "no entries found") ||
						strings.Contains(res, "no queries found") || strings.Contains(res, "no entity found") ||
						strings.Contains(res, "no wikipedia") || strings.Contains(res, "no definition found for") {
						knowledgeGapDetected = true
					}
				}
				resultMsg := "Tool result (" + discoveredToolName + "): " + toolResult.Result
				resp, err = session.SendMessage(ctx, &genai.Part{Text: utils.SanitizePrompt(resultMsg)})
				if err != nil {
					infra.ErrorsTotal.Inc()
					span.RecordError(err)
					return &QueryResult{
						Answer:     fmt.Sprintf("Error calling Gemini API: %v", infra.WrapLLMError(err)),
						Iterations: iteration,
						ToolCalls:  toolCalls,
						Error:      true,
						DebugLogs:  debugLogs,
					}
				}
				iteration++
				infra.GeminiCallsTotal.Inc()
				session.TrimHistory(MaxMessagePairs)
				logDebug("[iter %d] tool_result sent to LLM", iteration)
				continue
			}
			// hasCalls true with native FunctionCalls: fall through to native execution below
		} else {
			hasCalls = infra.HasFunctionCalls(resp)
			if !hasCalls {
				answerText = infra.ExtractTextFromResponse(resp)
			}
		}

		logDebug("[iter %d] LLM response: has_function_calls=%v", iteration, hasCalls)
		infra.LoggerFrom(ctx).Debug("FOH: iteration decision", "query_run_id", queryRunID, "phase", "decision", "iter", iteration, "has_function_calls", hasCalls, "llm_correlation_id", session.LastLLMCorrelationID(), "reason", "decompose: LLM either answers or calls tools")

		if !hasCalls {
			if answerText != "" {
				answer := strings.TrimSpace(answerText)

				if pass, reason, err := runReflectionCheck(ctx, app, answer, question); err == nil && !pass {
					infra.LoggerFrom(ctx).Debug("FOH: reflection check failed", "query_run_id", queryRunID, "phase", "reflection", "reason", reason, "action", "revising answer against semantic memory")
					logDebug("[reflect] failed: %s", reason)
					revised := runReflectionRevision(ctx, session, question, answer, reason)
					if revised != "" {
						answer = revised
						logDebug("[reflect] revised answer (%d chars)", len(answer))
					}
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

				// Synthesis pass: when multiple search results were used, refine the answer to avoid dumping/repetition.
				if searchToolCallCount > 1 || retrievedContent.Len() > 2000 {
					if refined, err := runSynthesisPass(ctx, app, question, answer, retrievedContent.String()); err == nil && strings.TrimSpace(refined) != "" {
						logDebug("[synthesis] applied synthesis pass (%d -> %d chars)", len(answer), len(refined))
						answer = refined
					}
				}

				if !strings.HasPrefix(answer, "Error:") {
					if err := EnqueueSaveQuery(ctx, question, answer, source, knowledgeGapDetected); err != nil {
						infra.LoggerFrom(ctx).Warn("failed to enqueue save-query task", "error", err)
					}
				}

				span.SetAttributes(map[string]string{
					"iterations": fmt.Sprintf("%d", iteration),
					"tool_calls": fmt.Sprintf("%d", len(toolCalls)),
					"answer_len": fmt.Sprintf("%d", len(answer)),
				})

				logDebug("[DEBUG] LLM Final Response: %q (%d chars) after %d iterations", answer, len(answer), iteration)
				return &QueryResult{
					Answer:     answer,
					Iterations: iteration,
					ToolCalls:  toolCalls,
					Error:      false,
					DebugLogs:  debugLogs,
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
					infra.ErrorsTotal.Inc()
					return &QueryResult{
						Answer:     fmt.Sprintf("Error calling Gemini API: %v", infra.WrapLLMError(err2)),
						Iterations: iteration,
						Error:      true,
						DebugLogs:  debugLogs,
					}
				}
				infra.GeminiCallsTotal.Inc()
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
					Answer:     "Saved.",
					Iterations: iteration,
					ToolCalls:  toolCalls,
					Error:      false,
					DebugLogs:  debugLogs,
				}
			}

			reason := infra.EmptyResponseReason(resp)
			infra.ErrorsTotal.Inc()
			logDebug("[error] No text or function calls in response: %s", reason)
			msg := "The model returned no content. This can happen occasionally; please try again."
			if !strings.Contains(reason, "Stop") {
				msg = fmt.Sprintf("Error: The model returned no content (%s). Please try again.", reason)
			}
			return &QueryResult{
				Answer:     msg,
				Iterations: iteration,
				Error:      true,
				DebugLogs:  debugLogs,
			}
		}

		toolNames := make([]string, 0, len(functionCalls))
		var sigParts []string
		for _, fc := range functionCalls {
			toolNames = append(toolNames, fc.Name)
			argsJSON, _ := json.Marshal(fc.Args)
			sigParts = append(sigParts, fmt.Sprintf("%s:%s", fc.Name, string(argsJSON)))
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
				infra.ToolCallsTotal.Inc()
				toolResult := tools.Execute(ctx, fcName, args)
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
			"consult_anthropologist": true, "consult_architect": true, "consult_executive": true, "consult_philosopher": true,
		}
		for _, r := range results {
			toolCalls = append(toolCalls, map[string]interface{}{
				"tool":           r.fcName,
				"arguments":      r.args,
				"success":        r.result.Success,
				"result_preview": utils.TruncateString(r.result.Result, 200),
			})

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
			infra.ErrorsTotal.Inc()
			span.RecordError(err)
			return &QueryResult{
				Answer:     fmt.Sprintf("Error calling Gemini API: %v", infra.WrapLLMError(err)),
				Iterations: iteration,
				ToolCalls:  toolCalls,
				Error:      true,
				DebugLogs:  debugLogs,
			}
		}
		iteration++
		infra.GeminiCallsTotal.Inc()
		session.TrimHistory(MaxMessagePairs)
		infra.LoggerFrom(ctx).Debug("FOH: tool results sent to LLM", "query_run_id", queryRunID, "phase", "tool_execution", "iter", iteration, "next_llm_correlation_id", session.LastLLMCorrelationID(), "reason", "reflect: next turn may answer or call more tools")
	}

	logDebug("[warning] Reached max iterations (%d), forcing conclusion", MaxIterations)
	infra.LoggerFrom(ctx).Debug("FOH: forcing conclusion", "query_run_id", queryRunID, "phase", "forced_conclusion", "iterations", MaxIterations, "reason", "max iterations reached; asking LLM for best answer so far")
	infra.LoggerFrom(ctx).Warn("query reached max iterations", "query_run_id", queryRunID, "phase", "forced_conclusion", "max", MaxIterations)

	resp, err = session.SendMessage(ctx, &genai.Part{Text: "Please provide your best answer based on the information gathered so far."})
	if err != nil {
		infra.ErrorsTotal.Inc()
		span.RecordError(err)
		return &QueryResult{
			Answer:     fmt.Sprintf("Error calling Gemini API: %v", infra.WrapLLMError(err)),
			Iterations: iteration,
			ToolCalls:  toolCalls,
			Error:      true,
			DebugLogs:  debugLogs,
		}
	}
	infra.GeminiCallsTotal.Inc()
	text := infra.ExtractTextFromResponse(resp)
	if text != "" {
		answer := strings.TrimSpace(text)
		if searchToolCallCount > 1 || retrievedContent.Len() > 2000 {
			if refined, err := runSynthesisPass(ctx, app, question, answer, retrievedContent.String()); err == nil && strings.TrimSpace(refined) != "" {
				answer = refined
			}
		}
		if !strings.HasPrefix(answer, "Error:") {
			_ = EnqueueSaveQuery(ctx, question, answer, source, knowledgeGapDetected)
		}
		forcedToolNames := make([]string, 0, len(toolCalls))
		for _, tc := range toolCalls {
			if t, ok := tc["tool"].(string); ok {
				forcedToolNames = append(forcedToolNames, t)
			}
		}
		infra.LogAssistantEfficiency(ctx, len(systemPrompt)+len(question), len(answer), iteration)
		infra.LoggerFrom(ctx).Debug("FOH: forced conclusion", "query_run_id", queryRunID, "phase", "forced_conclusion", "event", "query_complete", "question", question, "answer", answer, "iterations", iteration, "tool_call_count", len(toolCalls), "tool_names", strings.Join(forcedToolNames, ","), "duration_ms", time.Since(startTime).Milliseconds(), "forced_conclusion", true)
		span.SetAttributes(map[string]string{
			"iterations":        fmt.Sprintf("%d", iteration),
			"forced_conclusion": "true",
		})
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
	infra.ErrorsTotal.Inc()
	return &QueryResult{
		Answer:     "Error: Unable to complete query within iteration limits.",
		Iterations: iteration,
		ToolCalls:  toolCalls,
		Error:      true,
		DebugLogs:  debugLogs,
	}
}

const memoryQueryTemplate = "Permanent facts about: {{.Input}}"

func runReflectionCheck(ctx context.Context, app *infra.App, answer, question string) (pass bool, reason string, err error) {
	// Use question (user intent) for the memory query, not the answer. Otherwise for
	// short answers like "Logged." we search for "Permanent facts about: Logged."
	// and pull irrelevant entries instead of facts relevant to what the user said.
	memoryQuery := "Permanent facts about: "
	if len(question) > 200 {
		memoryQuery += question[:200]
	} else {
		memoryQuery += question
	}
	searchResult := tools.Execute(ctx, "semantic_search", map[string]interface{}{
		"query":       memoryQuery,
		"limit":       5,
		"source_text": question,
		"template":    memoryQueryTemplate,
	})
	semanticMemory := searchResult.Result
	if !searchResult.Success || semanticMemory == "" {
		semanticMemory = "(No semantic memory retrieved)"
	}

	if app == nil {
		return true, "", nil
	}
	prompt := prompts.FormatReflectionCheck(utils.SanitizePrompt(answer), utils.SanitizePrompt(semanticMemory))
	req := &infra.LLMRequest{
		Parts:     []*genai.Part{{Text: prompt}},
		Model:     app.Config().GeminiModel,
		GenConfig: &infra.GenConfig{MaxOutputTokens: 256},
	}
	infra.GeminiCallsTotal.Inc()
	resp, err := app.Dispatch(ctx, req)
	if err != nil {
		return true, "", err
	}
	text := strings.TrimSpace(infra.ExtractTextFromResponse(resp))
	simple, _ := utils.ParseKeyValueMap(text)
	passStr := strings.TrimSpace(strings.ToLower(simple["pass"]))
	pass = passStr == "true" || passStr == "yes" || passStr == "1"
	reason = strings.TrimSpace(simple["reason"])
	return pass, reason, nil
}

const synthesisPassRetrievedMaxBytes = 6000

// runSynthesisPass refines a candidate answer when multiple search results were merged, to reduce repetition and "dumping."
func runSynthesisPass(ctx context.Context, app *infra.App, question, candidateAnswer, retrievedContent string) (string, error) {
	if app == nil || app.Config() == nil {
		return candidateAnswer, fmt.Errorf("no app or config")
	}
	truncated := retrievedContent
	if len(truncated) > synthesisPassRetrievedMaxBytes {
		truncated = truncated[:synthesisPassRetrievedMaxBytes] + "\n... (truncated)"
	}
	userPrompt := fmt.Sprintf("User question:\n%s\n\nDraft answer (to refine):\n%s\n\nRetrieved content (merge and deduplicate):\n%s",
		utils.WrapAsUserData(utils.SanitizePrompt(question)),
		utils.WrapAsUserData(utils.SanitizePrompt(candidateAnswer)),
		utils.WrapAsUserData(utils.SanitizePrompt(truncated)))
	refined, err := infra.GenerateContentSimple(ctx, prompts.SynthesisPass()+prompts.DataSafety(), userPrompt, app.Config(), &infra.GenConfig{
		MaxOutputTokens: 1024,
		ModelOverride:   app.Config().GeminiModel,
	})
	if err != nil {
		return candidateAnswer, err
	}
	return strings.TrimSpace(refined), nil
}

func runReflectionRevision(ctx context.Context, session *infra.ChatSession, question, previousAnswer, reason string) string {
	searchResult := tools.Execute(ctx, "semantic_search", map[string]interface{}{
		"query": question,
		"limit": 10,
	})
	extraMemory := searchResult.Result
	if !searchResult.Success {
		extraMemory = "(Search failed)"
	}
	revisePrompt := fmt.Sprintf("Additional semantic memory:\n%s\n\nYour previous answer was flagged: %s\nPlease provide a revised final answer that avoids contradicting permanent facts and removes Gravel. Be concise.", utils.SanitizePrompt(extraMemory), utils.SanitizePrompt(reason))
	resp, err := session.SendMessage(ctx,
		&genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "semantic_search", Response: map[string]any{"result": utils.SanitizePrompt(extraMemory)}}},
		&genai.Part{Text: revisePrompt},
	)
	if err != nil {
		return ""
	}
	infra.GeminiCallsTotal.Inc()
	text := infra.ExtractTextFromResponse(resp)
	return strings.TrimSpace(text)
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
