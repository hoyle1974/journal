// Jot CLI - Thin client that calls cloud functions.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/timeout"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

// Configuration
var (
	APIBaseURL     string
	APIKey         string
	MachineName    string
	RequestTimeout = 30 * time.Second
)

func init() {
	// 1. Determine which .env file to load based on the profile
	profile := os.Getenv("JOT_PROFILE")
	envFile := ".env"
	if profile == "prod" {
		envFile = ".env.prod"
	}

	// 2. Load the specific environment file
	err := godotenv.Load(envFile)
	if err != nil && profile == "prod" {
		fmt.Printf("Warning: Failed to load %s. Ensure it exists for production use.\n", envFile)
	}

	// 3. Populate config vars
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
func apiRequest(ctx context.Context, method, endpoint string, data any, timeout time.Duration) (map[string]interface{}, error) {
	result, _, err := apiRequestWithHeaders(ctx, method, endpoint, data, timeout, false)
	return result, err
}

// apiRequestWithHeaders performs an HTTP request and returns the JSON response and response headers.
// When wantTrace is true, sends X-Want-Trace-Id so the backend exports this trace to Cloud Trace.
func apiRequestWithHeaders(ctx context.Context, method, endpoint string, data any, timeout time.Duration, wantTrace bool) (map[string]interface{}, http.Header, error) {
	url := APIBaseURL + endpoint

	var body io.Reader
	if data != nil {
		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, nil, err
		}
		body = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
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
			return nil, nil, fmt.Errorf("offline: %w", err)
		}
		if strings.Contains(err.Error(), "timeout") {
			return nil, nil, fmt.Errorf("timeout: %w", err)
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
		bodyStr := strings.TrimSpace(string(bodyBytes))
		if resp.StatusCode >= 400 && bodyStr != "" {
			// Server returned error body (e.g. 504 "upstream request timeout") that isn't JSON.
			if len(bodyStr) > 200 {
				bodyStr = bodyStr[:200] + "..."
			}
			return nil, nil, fmt.Errorf("request failed (status %d): %s", resp.StatusCode, bodyStr)
		}
		snippet := bodyStr
		if len(snippet) > 80 {
			snippet = snippet[:80] + "..."
		}
		return nil, nil, fmt.Errorf("invalid response (status %d): %w (body: %q)", resp.StatusCode, err, snippet)
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

// jsonStr extracts a string from a map safely.
func jsonStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// jsonStringSlice returns string elements from a JSON array field (e.g. reasoning_trace).
func jsonStringSlice(m map[string]interface{}, key string) []string {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// printDebugTraceIfAny prints the full chronological debug trace (prompt, reasoning, tool calls/results).
func printDebugTraceIfAny(result map[string]interface{}) bool {
	dt := jsonStringSlice(result, "debug_trace")
	if len(dt) == 0 {
		return false
	}
	fmt.Println("Debug Trace:")
	for i, entry := range dt {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("[%d] %s\n", i+1, strings.TrimSpace(entry))
	}
	fmt.Println()
	return true
}

func printResponseSeparatorIfDebug(hadDebug bool) {
	if hadDebug {
		fmt.Println("---------------------")
	}
}

// traceFlag is set when the user passes --trace or -t (parsed in main).
var traceFlag bool

// api wraps the API client; Do uses traceFlag and returns (result, headers, err). DoOrExit exits on error and prints trace when requested.
type apiClient struct{}

func (c *apiClient) Do(ctx context.Context, method, endpoint string, payload any, timeout time.Duration) (map[string]interface{}, http.Header, error) {
	if traceFlag {
		return apiRequestWithHeaders(ctx, method, endpoint, payload, timeout, true)
	}
	result, err := apiRequest(ctx, method, endpoint, payload, timeout)
	return result, nil, err
}

func (c *apiClient) DoOrExit(ctx context.Context, method, endpoint string, payload any, timeout time.Duration) (map[string]interface{}, http.Header) {
	result, headers, err := c.Do(ctx, method, endpoint, payload, timeout)
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

// apiPostLog sends a log entry to the API. When attachPath is non-empty, uses multipart/form-data with the image file.
func apiPostLog(ctx context.Context, content, source, attachPath string, timeout time.Duration) (map[string]interface{}, http.Header, error) {
	if attachPath == "" {
		return api.Do(ctx, "POST", "/log", map[string]string{"content": content, "source": source}, timeout)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("content", content)
	_ = w.WriteField("source", source)
	f, err := os.Open(attachPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open attachment: %w", err)
	}
	defer f.Close()
	fw, err := w.CreateFormFile("image", filepath.Base(attachPath))
	if err != nil {
		return nil, nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, nil, fmt.Errorf("write attachment: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", APIBaseURL+"/log", &buf)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if APIKey != "" {
		req.Header.Set("X-API-Key", APIKey)
	}
	if traceFlag {
		req.Header.Set("X-Want-Trace-Id", "true")
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	_ = json.Unmarshal(bodyBytes, &result)
	if resp.StatusCode >= 400 {
		errMsg := "unknown error"
		if s, ok := result["error"].(string); ok && s != "" {
			errMsg = s
		}
		return nil, nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errMsg)
	}
	return result, resp.Header.Clone(), nil
}

// =============================================================================
// COMMANDS
// =============================================================================

func cmdLog(content, attachPath string) {
	source := fmt.Sprintf("cli:%s", MachineName)
	result, headers, err := apiPostLog(context.Background(), content, source, attachPath, RequestTimeout)
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
	fmt.Println("Logged.")
}

func cmdIngest(input string) {
	result, headers, err := api.Do(context.Background(), "POST", "/ingest", map[string]string{
		"content": input,
		"source":  fmt.Sprintf("cli:%s", MachineName),
	}, time.Duration(timeout.QuerySeconds)*time.Second)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	if result == nil {
		fmt.Println("ok")
		return
	}
	if traceFlag && headers != nil {
		printTraceInfo(headers)
	}
	printResponseSeparatorIfDebug(printDebugTraceIfAny(result))
	if answer := jsonStr(result, "answer"); answer != "" {
		fmt.Println(answer)
	} else {
		fmt.Println("ok")
	}
}

func cmdEntries(limit int) {
	result, _ := api.DoOrExit(context.Background(), "GET", fmt.Sprintf("/entries?limit=%d", limit), nil, RequestTimeout)
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
		ts := memory.TruncateTimestamp(jsonStr(entry, "timestamp"), memory.DateTimeDisplayLen)
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
		result, headers, err := api.Do(context.Background(), "GET", fmt.Sprintf("/entries?limit=%d", limit), nil, RequestTimeout)
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

			ts := memory.TruncateTimestamp(jsonStr(entry, "timestamp"), memory.DateTimeDisplayLen)
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
			_, _, err = api.Do(context.Background(), "DELETE", "/entries", map[string]interface{}{
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

// =============================================================================
// MAIN
// =============================================================================

func buildRootCmd() *cobra.Command {
	var trace bool
	var attach string

	root := &cobra.Command{
		Use:   "jot [message]",
		Short: "Your personal AI assistant",
		Long: `Just type to your assistant:
  jot Had coffee with Sarah
  jot What did I do last week?
  jot I want to learn Japanese`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			traceFlag = trace
			maybePromptPendingQuestions()
			cmdIngest(strings.Join(args, " "))
			return nil
		},
	}
	root.PersistentFlags().BoolVarP(&trace, "trace", "t", false, "emit trace ID and Cloud Trace link")

	logCmd := &cobra.Command{
		Use:     "log [message]",
		Aliases: []string{"l"},
		Short:   "Fast log (bypasses AI)",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			traceFlag = trace
			content := strings.Join(args, " ")
			if len(content) > 10000 {
				fmt.Printf("Warning: Entry is very long (%d chars)\n", len(content))
			}
			cmdLog(content, attach)
			return nil
		},
	}
	logCmd.Flags().StringVar(&attach, "attach", "", "path to image file to attach")

	editCmd := &cobra.Command{
		Use:   "edit [limit]",
		Short: "Interactive entry editor",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			traceFlag = trace
			limit := 10
			if len(args) == 1 {
				n, err := strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("invalid limit: %s", args[0])
				}
				limit = n
			}
			cmdEdit(limit)
			return nil
		},
	}

	entriesCmd := &cobra.Command{
		Use:   "entries [limit]",
		Short: "List recent entries",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			traceFlag = trace
			limit := 10
			if len(args) == 1 {
				n, err := strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("invalid limit: %s", args[0])
				}
				limit = n
			}
			cmdEntries(limit)
			return nil
		},
	}

	dreamCmd := &cobra.Command{
		Use:   "dream",
		Short: "Run the dream cycle to synthesise recent activity into a summary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			traceFlag = trace
			cmdDream()
			return nil
		},
	}

	root.AddCommand(logCmd, editCmd, entriesCmd, dreamCmd)
	return root
}

func main() {
	root := buildRootCmd()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// maybePromptPendingQuestions fetches unresolved pending questions and prompts the user to answer or skip.
func maybePromptPendingQuestions() {
	if APIBaseURL == "" {
		return
	}
	result, err := apiRequest(context.Background(), "GET", "/pending-questions", nil, RequestTimeout)
	if err != nil || result == nil {
		return
	}
	questionsRaw, ok := result["questions"].([]interface{})
	if !ok || len(questionsRaw) == 0 {
		return
	}
	fmt.Println("\n--- Pending clarifications ---")
	for i, qRaw := range questionsRaw {
		q, _ := qRaw.(map[string]interface{})
		if q == nil {
			continue
		}
		kind := jsonStr(q, "kind")
		question := jsonStr(q, "question")
		questionCtx := jsonStr(q, "context")
		fmt.Printf("\n%d. [%s] %s\n", i+1, kind, question)
		if questionCtx != "" {
			fmt.Printf("   Context: %s\n", questionCtx)
		}
		fmt.Print("   Answer (or Enter to skip): ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(answer)
		if answer != "" {
			uuid := jsonStr(q, "uuid")
			if uuid != "" {
				_, _ = apiRequest(context.Background(), "POST", "/pending-questions/"+uuid+"/resolve", map[string]interface{}{"answer": answer}, RequestTimeout)
			}
		}
	}
	fmt.Println("---")
}

// cmdDream triggers the Dreamer background cycle via the API (force=true) and prints the result.
func cmdDream() {
	result, err := apiRequest(context.Background(), "POST", "/internal/dream",
		map[string]interface{}{"force": true}, 2*time.Minute)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	if skipped, _ := result["skipped"].(bool); skipped {
		reason, _ := result["skip_reason"].(string)
		fmt.Printf("Dream cycle skipped: %s\n", reason)
		return
	}
	summaryUUID, _ := result["summary_uuid"].(string)
	var questionCount int
	if qs, ok := result["questions"].([]interface{}); ok {
		questionCount = len(qs)
	}
	fmt.Printf("Dream cycle complete.\nSummary: %s\nQuestions enqueued: %d\n", summaryUUID, questionCount)
}
