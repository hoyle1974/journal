package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/tools"
)

const (
	MaxIterations       = 10
	MaxMessagePairs     = 20
	ToolRepeatBackOffAt = 3 // inject back-off prompt after same tool + similar args N times
)

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
func RunQuery(ctx context.Context, question, source string) *QueryResult {
	return RunQueryWithDebug(ctx, question, source, true) // Debug always on for now
}

// RunQueryWithDebug runs a query with optional debug logging.
func RunQueryWithDebug(ctx context.Context, question, source string, debug bool) *QueryResult {
	var debugLogs []string
	logDebug := func(msg string, args ...interface{}) {
		if debug {
			debugLogs = append(debugLogs, fmt.Sprintf(msg, args...))
		}
	}
	ctx, span := StartSpan(ctx, "query.run")
	defer span.End()

	startTime := time.Now()
	QueriesTotal.Inc()

	span.SetAttributes(map[string]string{
		"question_len": fmt.Sprintf("%d", len(question)),
		"source":       source,
	})

	if GeminiAPIKey == "" {
		ErrorsTotal.Inc()
		return &QueryResult{
			Answer:     "Error: GEMINI_API_KEY not configured",
			Iterations: 0,
			Error:      true,
		}
	}

	// Save user input to the journal immediately (deterministic system task; no LLM tool call).
	EntriesTotal.Inc()
	if _, err := AddEntry(ctx, question, source, nil); err != nil {
		LoggerFrom(ctx).Error("failed to log user input", "error", err)
		ErrorsTotal.Inc()
		span.RecordError(err)
		return &QueryResult{
			Answer:     fmt.Sprintf("Error saving input: %v", err),
			Iterations: 0,
			Error:      true,
			DebugLogs:  debugLogs,
		}
	}

	logDebug("[start] Question: %s", question)

	// Build system prompt with current date context
	systemPrompt := buildSystemPrompt(ctx)
	logDebug("[prompt] %s", systemPrompt)

	// Create chat session with tools
	toolDefs := tools.GetDefinitions()
	session, err := NewChatSession(ctx, systemPrompt, toolDefs)
	if err != nil {
		ErrorsTotal.Inc()
		span.RecordError(err)
		return &QueryResult{
			Answer:     fmt.Sprintf("Error creating chat session: %v", WrapLLMError(err)),
			Iterations: 0,
			Error:      true,
			DebugLogs:  debugLogs,
		}
	}
	logDebug("[init] Chat session created with %d tools", len(toolDefs))

	iteration := 0
	emptyContentRetries := 0
	var toolCalls []map[string]interface{}
	var lastToolCallSignature string       // Track previous tool calls to detect loops
	toolCallCounts := make(map[string]int) // "toolName:normArgs" -> count for back-off
	var repeatedToolName string            // set when back-off triggers (for prompt)
	var knowledgeGapDetected bool          // set when a search tool returns no results

	// Start the conversation with the user's question
	resp, err := session.SendMessage(ctx, genai.Text(question))
	if err != nil {
		ErrorsTotal.Inc()
		span.RecordError(err)
		return &QueryResult{
			Answer:     fmt.Sprintf("Error calling Gemini API: %v", WrapLLMError(err)),
			Iterations: 1,
			Error:      true,
			DebugLogs:  debugLogs,
		}
	}
	iteration++
	GeminiCallsTotal.Inc()
	logDebug("[iter %d] Sent question to LLM", iteration)

	// Agentic loop
	for iteration < MaxIterations {
		// Check if we have a final text response (no function calls)
		hasCalls := HasFunctionCalls(resp)
		logDebug("[iter %d] LLM response: has_function_calls=%v", iteration, hasCalls)

		if !hasCalls {
			text := ExtractText(resp)
			if text != "" {
				answer := strings.TrimSpace(text)

				// Reflection: check answer against semantic memory; if fail, one more search + revise
				if pass, reason, err := runReflectionCheck(ctx, answer, question); err == nil && !pass {
					logDebug("[reflect] failed: %s", reason)
					revised := runReflectionRevision(ctx, session, question, answer, reason)
					if revised != "" {
						answer = revised
						logDebug("[reflect] revised answer (%d chars)", len(answer))
					}
				}

				duration := time.Since(startTime)
				LoggerFrom(ctx).Info("query completed",
					"iterations", iteration,
					"tool_calls", len(toolCalls),
					"duration_ms", duration.Milliseconds(),
				)

				// Log the query via Cloud Task (fire-and-forget, non-critical)
				if !strings.HasPrefix(answer, "Error:") {
					if err := EnqueueTask(ctx, "/internal/save-query", map[string]interface{}{
						"question": question,
						"answer":   answer,
						"source":   source,
						"is_gap":   knowledgeGapDetected,
					}); err != nil {
						LoggerFrom(ctx).Warn("failed to enqueue save-query task", "error", err)
					}
				}

				span.SetAttributes(map[string]string{
					"iterations": fmt.Sprintf("%d", iteration),
					"tool_calls": fmt.Sprintf("%d", len(toolCalls)),
					"answer_len": fmt.Sprintf("%d", len(answer)),
				})

				logDebug("[done] Final answer (%d chars) after %d iterations", len(answer), iteration)
				return &QueryResult{
					Answer:     answer,
					Iterations: iteration,
					ToolCalls:  toolCalls,
					Error:      false,
					DebugLogs:  debugLogs,
				}
			}
		}

		// Process function calls
		functionCalls := ExtractFunctionCalls(resp)
		if len(functionCalls) == 0 {
			// Empty response (no text, no tool calls) — retry up to 2 times with short delay (Gemini sometimes returns Stop with no content)
			const maxEmptyRetries = 2
			if emptyContentRetries < maxEmptyRetries {
				emptyContentRetries++
				delay := time.Duration(emptyContentRetries) * time.Second
				logDebug("[retry] No text or function calls (attempt %d/%d), waiting %v then retrying", emptyContentRetries, maxEmptyRetries, delay)
				time.Sleep(delay)
				resp2, err2 := session.SendMessage(ctx, genai.Text(question))
				if err2 != nil {
					ErrorsTotal.Inc()
					return &QueryResult{
						Answer:     fmt.Sprintf("Error calling Gemini API: %v", WrapLLMError(err2)),
						Iterations: iteration,
						Error:      true,
						DebugLogs:  debugLogs,
					}
				}
				GeminiCallsTotal.Inc()
				if ExtractText(resp2) != "" || HasFunctionCalls(resp2) {
					resp = resp2
					logDebug("[retry] Retry had content, continuing")
					continue
				}
				resp = resp2
			}
			// Graceful fallback: if we've already executed a tool and the LLM returns empty
			// (e.g. trapped by conflicting instructions), treat the turn as complete.
			if iteration > 1 {
				logDebug("[done] LLM returned empty content after tool execution, defaulting to Logged.")
				return &QueryResult{
					Answer:     "Logged. (No further information found)",
					Iterations: iteration,
					ToolCalls:  toolCalls,
					Error:      false,
					DebugLogs:  debugLogs,
				}
			}

			reason := EmptyResponseReason(resp)
			ErrorsTotal.Inc()
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

		// Build signature of current tool calls to detect loops
		var sigParts []string
		for _, fc := range functionCalls {
			argsJSON, _ := json.Marshal(fc.Args)
			sigParts = append(sigParts, fmt.Sprintf("%s:%s", fc.Name, string(argsJSON)))
			logDebug("[iter %d] tool_call: %s(%s)", iteration, fc.Name, string(argsJSON))
		}
		currentSignature := strings.Join(sigParts, "|")

		// Detect if LLM is stuck in a loop (same tool calls as last iteration)
		if currentSignature == lastToolCallSignature && lastToolCallSignature != "" {
			logDebug("[warning] Detected tool call loop, forcing conclusion")
			LoggerFrom(ctx).Warn("detected tool call loop", "signature", truncateString(currentSignature, 100))
			// Force a text response by sending the results we already have
			break
		}
		lastToolCallSignature = currentSignature

		// Back-off: track same tool + similar args; if repeated 3 times, inject prompt to try different approach
		repeatedToolName = ""
		for _, fc := range functionCalls {
			key := fc.Name + ":" + normalizedToolArgs(fc.Args)
			toolCallCounts[key]++
			if toolCallCounts[key] >= ToolRepeatBackOffAt {
				repeatedToolName = fc.Name
				logDebug("[backoff] Tool %q called %d times with similar args", fc.Name, toolCallCounts[key])
				LoggerFrom(ctx).Warn("tool repeat back-off", "tool", fc.Name, "count", toolCallCounts[key])
				break
			}
		}

		// Execute function calls in parallel using worker pool
		type toolExecResult struct {
			index  int
			fcName string
			args   map[string]interface{}
			result ToolResult
		}

		results := make([]toolExecResult, len(functionCalls))
		var wg sync.WaitGroup
		var mu sync.Mutex

		for i, fc := range functionCalls {
			wg.Add(1)
			idx := i
			fcName := fc.Name

			// Convert Args to map[string]interface{} before spawning goroutine
			args := make(map[string]interface{})
			for k, v := range fc.Args {
				args[k] = v
			}

			// Execute via pool
			execFunc := func() {
				defer wg.Done()
				ToolCallsTotal.Inc()
				toolResult := tools.Execute(ctx, fcName, args)
				result := ToolResult{Success: toolResult.Success, Result: toolResult.Result}

				mu.Lock()
				results[idx] = toolExecResult{
					index:  idx,
					fcName: fcName,
					args:   args,
					result: result,
				}
				mu.Unlock()

				LoggerFrom(ctx).Debug("tool executed",
					"tool", fcName,
					"success", result.Success,
					"result_len", len(result.Result),
				)
				// Truncate result for debug display
				resultPreview := result.Result
				if len(resultPreview) > 500 {
					resultPreview = resultPreview[:500] + "..."
				}
				mu.Lock()
				logDebug("[iter %d] tool_result: %s -> %s", iteration, fcName, resultPreview)
				mu.Unlock()
			}

			app := GetApp(ctx)
			if app != nil {
				if err := app.SubmitToToolPool(execFunc); err != nil {
					// Pool submission failed, run synchronously
					wg.Done()
					wg.Add(1)
					execFunc()
				}
			} else {
				execFunc()
			}
		}
		wg.Wait()

		// Collect responses in original order; detect knowledge gap when search tools return no results
		var functionResponses []genai.Part
		searchToolCalled := false
		searchTools := map[string]bool{
			"semantic_search":           true,
			"get_entity_network":        true,
			"search_entries":            true,
			"get_entries_by_date_range": true,
			"wikipedia":                 true,
			"web_search":                true,
			"list_knowledge":            true,
			"consult_anthropologist":    true,
			"consult_architect":         true,
			"consult_executive":         true,
			"consult_philosopher":       true,
		}
		for _, r := range results {
			toolCalls = append(toolCalls, map[string]interface{}{
				"tool":           r.fcName,
				"arguments":      r.args,
				"success":        r.result.Success,
				"result_preview": truncateString(r.result.Result, 200),
			})

			functionResponses = append(functionResponses, genai.FunctionResponse{
				Name:     r.fcName,
				Response: map[string]any{"result": SanitizePrompt(r.result.Result)},
			})

			if searchTools[r.fcName] {
				searchToolCalled = true
			}
		}

		// Knowledge gap: if a search tool returned a no-results message, flag for SaveQuery
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

		// Optionally append back-off prompt to steer model away from repeated tool use
		messageParts := make([]genai.Part, 0, len(functionResponses)+1)
		for _, p := range functionResponses {
			messageParts = append(messageParts, p)
		}
		if repeatedToolName != "" {
			backOffPrompt := buildToolRepeatBackOffPrompt(repeatedToolName)
			messageParts = append(messageParts, genai.Text(backOffPrompt))
			// Clear counts for this tool so we don't inject the same prompt every iteration
			for k := range toolCallCounts {
				if strings.HasPrefix(k, repeatedToolName+":") {
					delete(toolCallCounts, k)
				}
			}
		}

		// Send function responses back to the model
		resp, err = session.SendMessage(ctx, messageParts...)
		if err != nil {
			ErrorsTotal.Inc()
			span.RecordError(err)
			logDebug("[error] Gemini API error: %v", err)
			return &QueryResult{
				Answer:     fmt.Sprintf("Error calling Gemini API: %v", WrapLLMError(err)),
				Iterations: iteration,
				ToolCalls:  toolCalls,
				Error:      true,
				DebugLogs:  debugLogs,
			}
		}
		iteration++
		GeminiCallsTotal.Inc()

		// Trim history if needed
		session.TrimHistory(MaxMessagePairs)
	}

	// Ran out of iterations - try to get final answer
	logDebug("[warning] Reached max iterations (%d), forcing conclusion", MaxIterations)
	LoggerFrom(ctx).Warn("query reached max iterations", "max", MaxIterations)

	resp, err = session.SendMessage(ctx, genai.Text("Please provide your best answer based on the information gathered so far."))
	if err != nil {
		ErrorsTotal.Inc()
		span.RecordError(err)
		logDebug("[error] Gemini API error on forced conclusion: %v", err)
		return &QueryResult{
			Answer:     fmt.Sprintf("Error calling Gemini API: %v", WrapLLMError(err)),
			Iterations: iteration,
			ToolCalls:  toolCalls,
			Error:      true,
			DebugLogs:  debugLogs,
		}
	}
	GeminiCallsTotal.Inc()
	text := ExtractText(resp)
	if text != "" {
		answer := strings.TrimSpace(text)
		if !strings.HasPrefix(answer, "Error:") {
			if err := EnqueueTask(ctx, "/internal/save-query", map[string]interface{}{
				"question": question,
				"answer":   answer,
				"source":   source,
				"is_gap":   knowledgeGapDetected,
			}); err != nil {
				LoggerFrom(ctx).Warn("failed to enqueue save-query task", "error", err)
			}
		}

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
	ErrorsTotal.Inc()
	return &QueryResult{
		Answer:     "Error: Unable to complete query within iteration limits.",
		Iterations: iteration,
		ToolCalls:  toolCalls,
		Error:      true,
		DebugLogs:  debugLogs,
	}
}

// looksLikeQuestion checks if the input looks like a question or information request.
func looksLikeQuestion(input string) bool {
	input = strings.ToLower(strings.TrimSpace(input))

	// Explicit question mark
	if strings.HasSuffix(input, "?") {
		return true
	}

	// Common question starters
	questionPrefixes := []string{
		"what ", "what's ", "whats ",
		"where ", "where's ", "wheres ",
		"when ", "when's ", "whens ",
		"who ", "who's ", "whos ",
		"why ", "why's ", "whys ",
		"how ", "how's ", "hows ",
		"which ", "whose ",
		"is ", "are ", "was ", "were ", "will ", "would ", "could ", "should ", "can ",
		"do ", "does ", "did ",
		"tell me ", "show me ", "find ", "search ", "look up ", "lookup ",
		"list ", "describe ", "explain ",
	}

	for _, prefix := range questionPrefixes {
		if strings.HasPrefix(input, prefix) {
			return true
		}
	}

	return false
}

// runReflectionCheck calls the LLM to verify the draft answer against semantic memory. Returns pass, reason, err.
func runReflectionCheck(ctx context.Context, answer, question string) (pass bool, reason string, err error) {
	memoryQuery := "Permanent facts about: "
	if len(answer) > 200 {
		memoryQuery += answer[:200]
	} else {
		memoryQuery += answer
	}
	searchResult := tools.Execute(ctx, "semantic_search", map[string]interface{}{
		"query": memoryQuery,
		"limit": 5,
	})
	semanticMemory := searchResult.Result
	if !searchResult.Success || semanticMemory == "" {
		semanticMemory = "(No semantic memory retrieved)"
	}

	client, err := GetGeminiClient(ctx)
	if err != nil {
		return true, "", err // on error, pass through (don't block)
	}
	model := client.GenerativeModel(GetEffectiveModel(ctx, GeminiModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(256)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"pass":   {Type: genai.TypeBoolean, Description: "true if the answer is consistent and not cluttered with Gravel"},
			"reason": {Type: genai.TypeString, Description: "Brief reason if pass is false"},
		},
		Required: []string{"pass", "reason"},
	}
	prompt := prompts.FormatReflectionCheck(SanitizePrompt(answer), SanitizePrompt(semanticMemory))
	GeminiCallsTotal.Inc()
	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return true, "", err
	}
	text := extractTextFromResponse(resp)
	var out struct {
		Pass   bool   `json:"pass"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(text), &out) != nil {
		return true, "", nil
	}
	return out.Pass, out.Reason, nil
}

// runReflectionRevision runs one more semantic search and asks the model for a revised answer.
func runReflectionRevision(ctx context.Context, session *ChatSession, question, previousAnswer, reason string) string {
	searchResult := tools.Execute(ctx, "semantic_search", map[string]interface{}{
		"query": question,
		"limit": 10,
	})
	extraMemory := searchResult.Result
	if !searchResult.Success {
		extraMemory = "(Search failed)"
	}
	revisePrompt := fmt.Sprintf("Additional semantic memory:\n%s\n\nYour previous answer was flagged: %s\nPlease provide a revised final answer that avoids contradicting permanent facts and removes Gravel. Be concise.", SanitizePrompt(extraMemory), SanitizePrompt(reason))
	resp, err := session.SendMessage(ctx,
		genai.FunctionResponse{Name: "semantic_search", Response: map[string]any{"result": SanitizePrompt(extraMemory)}},
		genai.Text(revisePrompt),
	)
	if err != nil {
		return ""
	}
	GeminiCallsTotal.Inc()
	text := ExtractText(resp)
	return strings.TrimSpace(text)
}

// GetAnswer is a simple wrapper that returns just the answer string (for sync compatibility).
func GetAnswer(ctx context.Context, question, source string) string {
	result := RunQuery(ctx, question, source)
	return result.Answer
}

// normalizedToolArgs returns a stable string for "similar" args (same query/topic) for repeat detection.
func normalizedToolArgs(args map[string]interface{}) string {
	queryKeys := []string{"query", "topic", "q", "search", "entity_name"}
	for _, k := range queryKeys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok {
				return strings.ToLower(strings.TrimSpace(s))
			}
		}
	}
	// Fallback: full args JSON so different args don't collapse
	b, _ := json.Marshal(args)
	return string(b)
}

// buildToolRepeatBackOffPrompt returns a short system prompt when the same tool was called 3 times with similar args.
func buildToolRepeatBackOffPrompt(toolName string) string {
	switch toolName {
	case "wikipedia":
		return "You have already called wikipedia several times with similar queries. Try a different approach: use web_search for current or general information, or summarize what you have found so far and give your best answer."
	case "semantic_search", "search_entries", "get_entries_by_date_range", "get_oldest_entries", "get_entity_network":
		return "You have already called " + toolName + " several times with similar arguments. Either use a different tool (e.g. web_search for external facts), or synthesize what you have and provide your best answer based on the information gathered."
	case "web_search":
		return "You have already called web_search several times. Summarize the results you have and provide your best answer; avoid repeating the same search."
	default:
		return "You have already called " + toolName + " several times with similar arguments. Try a different approach or provide your best answer based on the information you have gathered so far."
	}
}
