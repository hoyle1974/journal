// Jot CLI - Thin client that calls cloud functions.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackstrohm/jot/internal/timeout"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/joho/godotenv"
)

// Configuration
var (
	APIBaseURL     string
	APIKey         string
	MachineName    string
	RequestTimeout = 30 * time.Second
)

func init() {
	godotenv.Load()

	APIBaseURL = getEnv("JOT_API_URL", "")
	APIKey = os.Getenv("JOT_API_KEY")

	if name := os.Getenv("MACHINE_NAME"); name != "" {
		MachineName = name
	} else {
		host, _ := os.Hostname()
		MachineName = host
	}
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// apiRequest performs an HTTP request and returns the JSON response.
// Returns an error for 4xx/5xx status codes or network failures.
func apiRequest(method, endpoint string, data interface{}, timeout time.Duration) (map[string]interface{}, error) {
	result, _, err := apiRequestWithHeaders(method, endpoint, data, timeout, false)
	return result, err
}

// apiRequestWithHeaders performs an HTTP request and returns the JSON response and response headers.
// When wantTrace is true, sends X-Want-Trace-Id so the backend exports this trace to Cloud Trace.
func apiRequestWithHeaders(method, endpoint string, data interface{}, timeout time.Duration, wantTrace bool) (map[string]interface{}, http.Header, error) {
	url := APIBaseURL + endpoint

	var body io.Reader
	if data != nil {
		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, nil, err
		}
		body = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if APIKey != "" {
		req.Header.Set("X-API-Key", APIKey)
	}
	if wantTrace {
		req.Header.Set("X-Want-Trace-Id", "true")
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") ||
			strings.Contains(err.Error(), "no such host") ||
			strings.Contains(err.Error(), "network is unreachable") {
			return nil, nil, fmt.Errorf("offline")
		}
		if strings.Contains(err.Error(), "timeout") {
			return nil, nil, fmt.Errorf("timeout")
		}
		return nil, nil, err
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, nil, fmt.Errorf("read response: %w", readErr)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		snippet := string(bodyBytes)
		if len(snippet) > 80 {
			snippet = snippet[:80] + "..."
		}
		return nil, nil, fmt.Errorf("invalid response (status %d): %v (body: %q)", resp.StatusCode, err, snippet)
	}

	if resp.StatusCode >= 400 {
		errMsg := "unknown error"
		if s, ok := result["error"].(string); ok && s != "" {
			errMsg = s
		}
		return nil, nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errMsg)
	}

	return result, resp.Header.Clone(), nil
}

// printTraceInfo writes the Trace ID and Cloud Trace console link to stderr when headers contain X-Trace-Id.
func printTraceInfo(headers http.Header) {
	traceID := headers.Get("X-Trace-Id")
	if traceID == "" {
		fmt.Fprintln(os.Stderr, "Trace ID not returned by server")
		return
	}
	project := headers.Get("X-Cloud-Project")
	fmt.Fprintf(os.Stderr, "Trace ID: %s\n", traceID)
	if project != "" {
		fmt.Fprintf(os.Stderr, "Console: https://console.cloud.google.com/traces/explorer?project=%s\n", project)
	} else {
		fmt.Fprintln(os.Stderr, "Console: https://console.cloud.google.com/traces/explorer (select project)")
	}
}

// jsonFloat extracts a float64 from a map, handling JSON number types.
func jsonFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

// jsonStr extracts a string from a map safely.
func jsonStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// parseTraceFlag removes --trace and -t from args and returns the filtered args and whether trace was set.
func parseTraceFlag(args []string) ([]string, bool) {
	var out []string
	var trace bool
	for _, a := range args {
		if a == "--trace" || a == "-t" {
			trace = true
			continue
		}
		out = append(out, a)
	}
	return out, trace
}

// traceFlag is set when the user passes --trace or -t (parsed in main).
var traceFlag bool

// api wraps the API client; Do uses traceFlag and returns (result, headers, err). DoOrExit exits on error and prints trace when requested.
type apiClient struct{}

func (c *apiClient) Do(method, endpoint string, payload interface{}, timeout time.Duration) (map[string]interface{}, http.Header, error) {
	if traceFlag {
		return apiRequestWithHeaders(method, endpoint, payload, timeout, true)
	}
	result, err := apiRequest(method, endpoint, payload, timeout)
	return result, nil, err
}

func (c *apiClient) DoOrExit(method, endpoint string, payload interface{}, timeout time.Duration) (map[string]interface{}, http.Header) {
	result, headers, err := c.Do(method, endpoint, payload, timeout)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	if result == nil {
		fmt.Println("Error: No response from API")
		os.Exit(1)
	}
	if traceFlag && headers != nil {
		printTraceInfo(headers)
	}
	return result, headers
}

var api = &apiClient{}

// =============================================================================
// COMMANDS
// =============================================================================

func cmdLog(content string) {
	source := fmt.Sprintf("cli:%s", MachineName)
	api.DoOrExit("POST", "/log", map[string]string{"content": content, "source": source}, RequestTimeout)
	fmt.Println("Logged.")
}

func cmdQuery(question string) {
	result, headers, err := api.Do("POST", "/query", map[string]string{
		"question": question,
		"source":   fmt.Sprintf("cli:%s", MachineName),
	}, time.Duration(timeout.QuerySeconds)*time.Second)
	if err != nil {
		if err.Error() == "offline" {
			fmt.Println("Error: Cannot query while offline. Queries require cloud connection.")
		} else {
			fmt.Printf("Error: %v\n", err)
		}
		os.Exit(1)
	}
	if result == nil {
		fmt.Println("Error: No response from API")
		os.Exit(1)
	}
	if traceFlag && headers != nil {
		printTraceInfo(headers)
	}

	// Check for error - can be string (auth error) or bool (query error)
	if errStr := jsonStr(result, "error"); errStr != "" {
		fmt.Printf("Error: %s\n", errStr)
		os.Exit(1)
	}
	if errFlag, ok := result["error"].(bool); ok && errFlag {
		if debugLogs, ok := result["debug_logs"].([]interface{}); ok && len(debugLogs) > 0 {
			fmt.Println("--- Debug Logs ---")
			for _, log := range debugLogs {
				if logStr, ok := log.(string); ok {
					fmt.Println(logStr)
				}
			}
			fmt.Println("------------------")
		}
		if answer := jsonStr(result, "answer"); answer != "" {
			fmt.Printf("%s\n", answer)
		} else {
			fmt.Println("Error: Query failed")
		}
		os.Exit(1)
	}

	if debugLogs, ok := result["debug_logs"].([]interface{}); ok && len(debugLogs) > 0 {
		fmt.Println("--- Debug Logs ---")
		for _, log := range debugLogs {
			if logStr, ok := log.(string); ok {
				fmt.Println(logStr)
			}
		}
		fmt.Println("------------------")
	}

	if answer := jsonStr(result, "answer"); answer != "" {
		fmt.Println(answer)
	} else {
		fmt.Println("No answer received")
	}
}

func cmdSync() {
	fmt.Println("Syncing Google Doc...")
	result, headers := api.DoOrExit("POST", "/sync", nil, 300*time.Second)
	if traceFlag && headers != nil {
		printTraceInfo(headers)
	}
	if msg := jsonStr(result, "message"); msg != "" {
		fmt.Printf("  %s\n", msg)
		return
	}

	entries := int(jsonFloat(result, "entries_added"))
	questions := int(jsonFloat(result, "questions_answered"))
	actions := int(jsonFloat(result, "actions_executed"))
	todosCompleted := int(jsonFloat(result, "todos_completed"))
	todosDeleted := int(jsonFloat(result, "todos_deleted"))
	total := entries + questions + actions + todosCompleted + todosDeleted

	if total == 0 {
		fmt.Println("  Nothing to process")
	} else {
		fmt.Printf("  Processed: %d entries, %d questions, %d actions, %d todos done, %d todos deleted\n",
			entries, questions, actions, todosCompleted, todosDeleted)
	}
}

func cmdEntries(limit int) {
	result, _ := api.DoOrExit("GET", fmt.Sprintf("/entries?limit=%d", limit), nil, RequestTimeout)
	entriesRaw, ok := result["entries"].([]interface{})
	if !ok || len(entriesRaw) == 0 {
		fmt.Println("No entries found.")
		return
	}

	for i, entryRaw := range entriesRaw {
		entry, ok := entryRaw.(map[string]interface{})
		if !ok {
			continue
		}
		ts := journal.TruncateTimestamp(jsonStr(entry, "timestamp"), journal.DateTimeDisplayLen)
		source := jsonStr(entry, "source")
		content := jsonStr(entry, "content")

		if len(content) > 80 {
			content = content[:77] + "..."
		}

		fmt.Printf("%d. [%s] (%s)\n", i+1, ts, source)
		fmt.Printf("   %s\n\n", content)
	}
}

func cmdEdit(limit int) {
	firstFetch := true
	for {
		result, headers, err := api.Do("GET", fmt.Sprintf("/entries?limit=%d", limit), nil, RequestTimeout)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		if result == nil {
			fmt.Println("Error: No response from API")
			return
		}
		if traceFlag && headers != nil && firstFetch {
			printTraceInfo(headers)
			firstFetch = false
		}
		entriesRaw, ok := result["entries"].([]interface{})
		if !ok || len(entriesRaw) == 0 {
			fmt.Println("No entries found.")
			return
		}

		fmt.Printf("\n%s\n", strings.Repeat("=", 60))
		fmt.Printf("Last %d entries:\n", len(entriesRaw))
		fmt.Printf("%s\n\n", strings.Repeat("=", 60))

		entries := make([]map[string]interface{}, 0, len(entriesRaw))
		for i, entryRaw := range entriesRaw {
			entry, ok := entryRaw.(map[string]interface{})
			if !ok {
				continue
			}
			entries = append(entries, entry)

			ts := journal.TruncateTimestamp(jsonStr(entry, "timestamp"), journal.DateTimeDisplayLen)
			content := jsonStr(entry, "content")
			if len(content) > 60 {
				content = content[:57] + "..."
			}
			fmt.Printf("  %d. [%s] %s\n", i+1, ts, content)
		}

		fmt.Printf("\n%s\n", strings.Repeat("=", 60))
		fmt.Println("Commands: d <#> (delete), v <#> (view), r (refresh), q (quit)")
		fmt.Printf("%s\n", strings.Repeat("=", 60))

		fmt.Print("\n> ")
		var cmd string
		fmt.Scanln(&cmd)
		cmd = strings.TrimSpace(strings.ToLower(cmd))

		if cmd == "" || cmd == "q" {
			return
		}
		if cmd == "r" {
			continue
		}
		if strings.HasPrefix(cmd, "d ") {
			idxStr := strings.TrimPrefix(cmd, "d ")
			idx, err := strconv.Atoi(idxStr)
			if err != nil || idx < 1 || idx > len(entries) {
				fmt.Println("Invalid index")
				continue
			}
			entryUUID := jsonStr(entries[idx-1], "uuid")
			if entryUUID == "" {
				fmt.Println("Invalid entry")
				continue
			}
			_, _, err = api.Do("DELETE", "/entries", map[string]interface{}{
				"uuids": []string{entryUUID},
			}, RequestTimeout)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Deleted.")
			}
		} else if strings.HasPrefix(cmd, "v ") {
			idxStr := strings.TrimPrefix(cmd, "v ")
			idx, err := strconv.Atoi(idxStr)
			if err != nil || idx < 1 || idx > len(entries) {
				fmt.Println("Invalid index")
				continue
			}
			entry := entries[idx-1]
			fmt.Printf("\n%s\n", strings.Repeat("=", 60))
			fmt.Printf("UUID: %s\n", jsonStr(entry, "uuid"))
			fmt.Printf("Time: %s\n", jsonStr(entry, "timestamp"))
			fmt.Printf("Source: %s\n", jsonStr(entry, "source"))
			fmt.Printf("%s\n", strings.Repeat("=", 60))
			fmt.Println(jsonStr(entry, "content"))
			fmt.Printf("%s\n\n", strings.Repeat("=", 60))
		} else {
			fmt.Println("Unknown command")
		}
	}
}

func cmdDream() {
	fmt.Println("Running Dreamer (consolidating last 24h into semantic memory)...")
	result, headers, err := api.Do("POST", "/dream", nil, time.Duration(timeout.QuerySeconds)*time.Second)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "Rate limit") || strings.Contains(err.Error(), "quota") {
			fmt.Println("Tip: You may have hit a billing or rate limit. Check Google AI Studio (aistudio.google.com) or try again later.")
		}
		os.Exit(1)
	}
	if result == nil {
		fmt.Println("Error: No response from API")
		os.Exit(1)
	}
	if traceFlag && headers != nil {
		printTraceInfo(headers)
	}
	entries := int(jsonFloat(result, "entries_processed"))
	extracted := int(jsonFloat(result, "facts_extracted"))
	written := int(jsonFloat(result, "facts_written"))
	fmt.Printf("Entries: %d | Extracted: %d | Written: %d\n", entries, extracted, written)
}

func cmdRollup() {
	fmt.Println("Running roll-up (weekly + monthly summaries)...")
	result, _ := api.DoOrExit("POST", "/rollup", nil, time.Duration(timeout.QuerySeconds)*time.Second)
	weekly := int(jsonFloat(result, "weekly_entries_rolled"))
	monthly := int(jsonFloat(result, "monthly_weekly_nodes"))
	fmt.Printf("Weekly entries rolled: %d | Monthly (weekly nodes): %d\n", weekly, monthly)
}

func cmdJanitor() {
	fmt.Println("Running Janitor (garbage collection)...")
	result, headers, err := api.Do("POST", "/janitor", nil, time.Duration(timeout.QuerySeconds)*time.Second)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "Rate limit") || strings.Contains(err.Error(), "quota") {
			fmt.Println("Tip: You may have hit a billing or rate limit. Check Google AI Studio (aistudio.google.com) or try again later.")
		}
		os.Exit(1)
	}
	if result == nil {
		fmt.Println("Error: No response from API")
		os.Exit(1)
	}
	if traceFlag && headers != nil {
		printTraceInfo(headers)
	}
	if deleted, ok := result["deleted"].(float64); ok {
		fmt.Printf("Evicted: %.0f\n", deleted)
	}

	if errors, ok := result["errors"].([]interface{}); ok && len(errors) > 0 {
		fmt.Printf("\nErrors (%d):\n", len(errors))
		for i, e := range errors {
			if i >= 5 {
				break
			}
			fmt.Printf("  - %v\n", e)
		}
	}
}

func cmdPlan(goal string) {
	fmt.Println("Generating plan (this takes a few seconds)...")
	result, headers, err := api.Do("POST", "/plan", map[string]string{"goal": goal}, time.Duration(timeout.QuerySeconds)*time.Second)
	if err != nil {
		if err.Error() == "offline" {
			fmt.Println("Error: Cannot generate plans while offline. Requires cloud connection.")
		} else {
			fmt.Printf("Error: %v\n", err)
		}
		os.Exit(1)
	}
	if result == nil {
		fmt.Println("Error: No response from API")
		os.Exit(1)
	}
	if traceFlag && headers != nil {
		printTraceInfo(headers)
	}
	if errStr := jsonStr(result, "error"); errStr != "" {
		fmt.Printf("Error: %s\n", errStr)
		os.Exit(1)
	}

	if plan := jsonStr(result, "plan"); plan != "" {
		fmt.Printf("\n%s\n", plan)
	} else {
		fmt.Println("Plan generation completed, but no text was returned.")
	}
}

func cmdHelp(topic string) {
	topics := map[string]string{
		"log": `
jot log <message>
jot l <message>

  Log a journal entry. Requires internet connection.

  Example: jot log Had a great meeting with the team today
`,
		"query": `
jot query <question>
jot q <question>

  Ask a question about your journal using AI. Requires internet.

  Examples:
    jot query What did I do last week?
    jot q What meetings did I have in January?
    jot query How has my mood been lately?
`,
		"sync": `
jot sync
jot s

  Process the linked Google Doc.
  - Processes new entries from the Google Doc
  - Answers any questions (?question) in the Google Doc
`,
		"edit": `
jot edit [limit]

  Interactive mode to view and delete entries.

  Commands in edit mode:
    d <#>  - Delete entry
    v <#>  - View full entry
    r      - Refresh list
    q      - Quit
`,
		"dream": `
jot dream

  Run the Dreamer: consolidate last 24h of journal entries into semantic memory.
  Extracts "gold" (permanent facts) from "gravel" (temporary logistics).
  Schedule daily via Cloud Scheduler.
`,
		"janitor": `
jot janitor

  Run garbage collection: evict low-significance semantic memory entries
  that haven't been recalled in 30+ days. Schedule weekly.
`,
		"plan": `
jot plan <goal>
jot p <goal>

  Break down a complex goal into a structured, step-by-step plan.
  The goal and all phases are saved to your knowledge graph for later recall.

  Examples:
    jot plan Rebuild my home server setup
    jot p Learn Rust programming over the next 3 months
    jot plan Migrate infrastructure to AWS

  Later, you can query your plans:
    jot query What was phase 2 of my server rebuild?
    jot query What are my pending project goals?
`,
	}

	if topic != "" {
		if help, ok := topics[topic]; ok {
			fmt.Println(help)
		} else {
			fmt.Printf("Unknown topic: %s\n", topic)
			fmt.Printf("Available: %s\n", strings.Join(slices.Collect(maps.Keys(topics)), ", "))
		}
	} else {
		fmt.Print(`
Jot - Personal Assistant CLI

Usage: jot <anything>

Just talk to your assistant - it figures out what to do:
  jot Had coffee with Sarah        → Logs entry + extracts knowledge
  jot What did I do last week?     → Searches and answers
  jot Who knows about GCP?         → Semantic search of knowledge
  jot I want to learn Japanese     → Creates structured plan
  jot Remember Alice works at Google → Saves to long-term memory

Commands (optional):
  log, l <message>     Fast logging (bypasses AI)
  plan, p <goal>       Direct plan generation
  sync, s              Process Google Doc
  edit [limit]         Interactive entry editor
  entries [limit]      List recent entries
  dream, janitor       Run Dreamer (daily) or Janitor (weekly) cron
  help [topic]         Show help

Run 'jot help <topic>' for detailed help.
`)
	}
}

// =============================================================================
// MAIN
// =============================================================================

func main() {
	args, trace := parseTraceFlag(os.Args[1:])
	traceFlag = trace

	if len(args) == 0 {
		cmdHelp("")
		os.Exit(1)
	}

	cmd := strings.ToLower(args[0])

	// Before starting a journal/query session, check for pending clarification questions
	if cmd == "log" || cmd == "l" || cmd == "query" || cmd == "q" {
		maybePromptPendingQuestions()
	}

	switch cmd {
	case "log", "l":
		if len(args) < 2 {
			fmt.Println("Error: content is required")
			os.Exit(1)
		}
		content := strings.Join(args[1:], " ")
		if len(content) > 10000 {
			fmt.Printf("Warning: Entry is very long (%d chars)\n", len(content))
		}
		cmdLog(content)

	case "query", "q":
		if len(args) < 2 {
			fmt.Println("Error: input is required")
			os.Exit(1)
		}
		input := strings.Join(args[1:], " ")
		cmdQuery(input)

	case "sync", "s":
		cmdSync()

	case "edit":
		limit := 10
		if len(args) > 1 {
			if parsed, err := strconv.Atoi(args[1]); err == nil {
				limit = parsed
			} else {
				fmt.Printf("Invalid number: %s\n", args[1])
				os.Exit(1)
			}
		}
		cmdEdit(limit)

	case "entries":
		limit := 10
		if len(args) > 1 {
			if parsed, err := strconv.Atoi(args[1]); err == nil {
				limit = parsed
			} else {
				fmt.Printf("Invalid number: %s\n", args[1])
				os.Exit(1)
			}
		}
		cmdEntries(limit)

	case "dream":
		cmdDream()
	case "janitor":
		cmdJanitor()
	case "rollup":
		cmdRollup()

	case "plan", "p":
		if len(args) < 2 {
			fmt.Println("Error: goal is required")
			os.Exit(1)
		}
		goal := strings.Join(args[1:], " ")
		cmdPlan(goal)

	case "help", "-h", "--help":
		topic := ""
		if len(args) > 1 {
			topic = args[1]
		}
		cmdHelp(topic)

	default:
		maybePromptPendingQuestions()
		input := strings.Join(args, " ")
		cmdQuery(input)
	}
}

// maybePromptPendingQuestions fetches unresolved pending questions and prompts the user to answer or skip.
func maybePromptPendingQuestions() {
	if APIBaseURL == "" {
		return
	}
	result, err := apiRequest("GET", "/pending-questions", nil, RequestTimeout)
	if err != nil || result == nil {
		return
	}
	questionsRaw, ok := result["questions"].([]interface{})
	if !ok || len(questionsRaw) == 0 {
		return
	}
	fmt.Println("\n--- Pending clarifications (from your last dream run) ---")
	for i, qRaw := range questionsRaw {
		q, _ := qRaw.(map[string]interface{})
		if q == nil {
			continue
		}
		kind := jsonStr(q, "kind")
		question := jsonStr(q, "question")
		context := jsonStr(q, "context")
		fmt.Printf("\n%d. [%s] %s\n", i+1, kind, question)
		if context != "" {
			fmt.Printf("   Context: %s\n", context)
		}
		fmt.Print("   Answer (or Enter to skip): ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(answer)
		if answer != "" {
			uuid := jsonStr(q, "uuid")
			if uuid != "" {
				_, _ = apiRequest("POST", "/pending-questions/"+uuid+"/resolve", map[string]interface{}{"answer": answer}, RequestTimeout)
			}
		}
	}
	fmt.Println("---")
}
