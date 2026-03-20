package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackstrohm/jot/internal/infra"
	"google.golang.org/genai"
)

func TestMain(m *testing.M) {
	registerTestTools()
	os.Exit(m.Run())
}

func registerTestTools() {
	type SearchArgs struct {
		Query string `json:"query" description:"Search query" required:"true"`
	}
	Register(&Tool{
		Name: "test_knowledge_search", Category: "knowledge",
		Description: "Search knowledge base. Returns matching facts.",
		Args:        &SearchArgs{},
		Execute:     func(ctx context.Context, env infra.ToolEnv, args any) Result { return OK("ok") },
	})
	Register(&Tool{
		Name: "test_task_create", Category: "task",
		Description: "Create a task. Adds it to the task list.",
		Args:        &SearchArgs{},
		Execute:     func(ctx context.Context, env infra.ToolEnv, args any) Result { return OK("ok") },
	})
	Register(&Tool{
		Name: "test_web_fetch", Category: "web",
		Description: "Fetch a URL. Gets page content.",
		Args:        &SearchArgs{},
		Execute:     func(ctx context.Context, env infra.ToolEnv, args any) Result { return OK("ok") },
	})
}

func TestArgsString(t *testing.T) {
	args := NewArgs(map[string]interface{}{
		"name":  "test",
		"empty": "",
	})

	tests := []struct {
		name     string
		key      string
		def      string
		expected string
	}{
		{"existing key", "name", "default", "test"},
		{"missing key", "missing", "default", "default"},
		{"empty value", "empty", "default", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := args.String(tt.key, tt.def)
			if result != tt.expected {
				t.Errorf("String(%q, %q) = %q, want %q", tt.key, tt.def, result, tt.expected)
			}
		})
	}
}

func TestArgsRequiredString(t *testing.T) {
	args := NewArgs(map[string]interface{}{
		"name": "test",
	})

	// Existing key
	val, ok := args.RequiredString("name")
	if !ok || val != "test" {
		t.Errorf("RequiredString(name) = %q, %v; want test, true", val, ok)
	}

	// Missing key
	val, ok = args.RequiredString("missing")
	if ok || val != "" {
		t.Errorf("RequiredString(missing) = %q, %v; want '', false", val, ok)
	}
}

func TestArgsIntBounded(t *testing.T) {
	args := NewArgs(map[string]interface{}{
		"count": float64(25), // JSON unmarshals numbers as float64
		"low":   float64(-5),
		"high":  float64(100),
	})

	tests := []struct {
		name     string
		key      string
		def      int
		min      int
		max      int
		expected int
	}{
		{"existing within bounds", "count", 10, 1, 50, 25},
		{"missing uses default", "missing", 10, 1, 50, 10},
		{"below minimum", "low", 10, 1, 50, 1},
		{"above maximum", "high", 10, 1, 50, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := args.IntBounded(tt.key, tt.def, tt.min, tt.max)
			if result != tt.expected {
				t.Errorf("IntBounded(%q, %d, %d, %d) = %d, want %d",
					tt.key, tt.def, tt.min, tt.max, result, tt.expected)
			}
		})
	}
}

func TestResultHelpers(t *testing.T) {
	// Test OK
	okResult := OK("Found %d items", 5)
	if !okResult.Success {
		t.Error("OK() should set Success to true")
	}
	if okResult.Result != "Found 5 items" {
		t.Errorf("OK() Result = %q, want 'Found 5 items'", okResult.Result)
	}

	// Test Fail
	failResult := Fail("Error: %s", "something went wrong")
	if failResult.Success {
		t.Error("Fail() should set Success to false")
	}
	if failResult.Result != "Error: something went wrong" {
		t.Errorf("Fail() Result = %q, want 'Error: something went wrong'", failResult.Result)
	}

	// Test MissingParam
	missingResult := MissingParam("query")
	if missingResult.Success {
		t.Error("MissingParam() should set Success to false")
	}
	if missingResult.Result != "Missing required parameter: query" {
		t.Errorf("MissingParam() Result = %q", missingResult.Result)
	}
}

func TestStructToGenaiSchema(t *testing.T) {
	type ExampleArgs struct {
		Query   string `json:"query" description:"Search query" required:"true"`
		Limit   int    `json:"limit" description:"Max results" default:"10"`
		Action  string `json:"action" description:"Action" required:"true" enum:"a,b,c"`
	}
	schema := StructToGenaiSchema(&ExampleArgs{})
	if schema.Type != genai.TypeObject {
		t.Errorf("schema.Type = %v, want TypeObject", schema.Type)
	}
	if len(schema.Properties) != 3 {
		t.Errorf("schema.Properties has %d keys, want 3", len(schema.Properties))
	}
	if len(schema.Required) != 2 {
		t.Errorf("schema.Required has %d items, want 2 (query, action)", len(schema.Required))
	}
}

func TestRegistryExecuteUnknownTool(t *testing.T) {
	result := Execute(context.Background(), nil, "nonexistent_tool", nil)
	if result.Success {
		t.Error("Execute for unknown tool should return failure")
	}
	if result.Result != "Unknown tool: nonexistent_tool" {
		t.Errorf("Execute result = %q", result.Result)
	}
}

func TestGetDefinitionsReturnsSlice(t *testing.T) {
	// Tool registration happens when a main binary imports impl (e.g. cmd/local).
	// In isolation this package has no tools registered.
	defs := GetDefinitions()
	if defs == nil {
		t.Error("GetDefinitions() returned nil")
	}
}

// --- firstSentence tests ---

func TestFirstSentenceEmpty(t *testing.T) {
	if got := firstSentence("", 80); got != "" {
		t.Errorf("firstSentence('') = %q, want ''", got)
	}
}

func TestFirstSentencePeriod(t *testing.T) {
	got := firstSentence("Hello world. More text.", 80)
	if got != "Hello world." {
		t.Errorf("firstSentence with period = %q, want 'Hello world.'", got)
	}
}

func TestFirstSentenceNewline(t *testing.T) {
	// newline is a sentence boundary; TrimSpace removes it from the result
	got := firstSentence("First line\nSecond", 80)
	if got != "First line" {
		t.Errorf("firstSentence with newline = %q, want 'First line'", got)
	}
}

func TestFirstSentenceNoEndShorterThanMax(t *testing.T) {
	got := firstSentence("Short string", 80)
	if got != "Short string" {
		t.Errorf("firstSentence short = %q, want 'Short string'", got)
	}
}

func TestFirstSentenceNoEndLongerThanMax(t *testing.T) {
	long := strings.Repeat("x", 100)
	got := firstSentence(long, 80)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("firstSentence long = %q, want trailing '...'", got)
	}
	if len(got) != 83 { // 80 chars + "..."
		t.Errorf("firstSentence long len = %d, want 83", len(got))
	}
}

func TestFirstSentencePeriodAfterMaxLen(t *testing.T) {
	// Period exists but comes after maxLen — result should be truncated with "..."
	s := strings.Repeat("x", 90) + ". tail"
	got := firstSentence(s, 80)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("firstSentence period-after-max = %q, want trailing '...'", got)
	}
}

// containsToolNamed reports whether any ToolSummary in results has the given name.
func containsToolNamed(results []ToolSummary, name string) bool {
	for _, r := range results {
		if r.Name == name {
			return true
		}
	}
	return false
}

// --- SearchRegistry tests ---

func TestSearchRegistryEmptyQuery(t *testing.T) {
	results := SearchRegistry("", 8)
	// empty query: words = [""], len("") < 2 → no scores → empty
	if len(results) != 0 {
		t.Errorf("SearchRegistry('') = %d results, want 0", len(results))
	}
}

func TestSearchRegistryNameMatch(t *testing.T) {
	results := SearchRegistry("test_knowledge_search", 8)
	if !containsToolNamed(results, "test_knowledge_search") {
		t.Error("SearchRegistry('test_knowledge_search') did not return test_knowledge_search")
	}
}

func TestSearchRegistryCategoryMatch(t *testing.T) {
	results := SearchRegistry("knowledge", 8)
	if !containsToolNamed(results, "test_knowledge_search") {
		t.Error("SearchRegistry('knowledge') did not return test_knowledge_search")
	}
}

func TestSearchRegistryLimit(t *testing.T) {
	// "test" matches all three test tools; limit=1 should return only 1
	results := SearchRegistry("test", 1)
	if len(results) != 1 {
		t.Errorf("SearchRegistry('test', limit=1) = %d results, want 1", len(results))
	}
}

func TestSearchRegistryDefaultLimit(t *testing.T) {
	// limit=0 defaults to 8; we have 3 test tools so all should come back
	results := SearchRegistry("test", 0)
	if len(results) < 3 {
		t.Errorf("SearchRegistry('test', 0) = %d results, want at least 3", len(results))
	}
}

func TestSearchRegistryExcludesDiscoverySearch(t *testing.T) {
	// Register a fake discovery_search to test exclusion (will panic if already registered)
	// Instead, just verify the constant name is excluded by searching for it directly.
	results := SearchRegistry(discoverySearchName, 8)
	for _, r := range results {
		if r.Name == discoverySearchName {
			t.Error("SearchRegistry returned discovery_search, which should be excluded")
		}
	}
}

func TestSearchRegistryScoreOrdering(t *testing.T) {
	// "knowledge" appears in the name of test_knowledge_search → higher score than a description-only match
	results := SearchRegistry("knowledge search", 8)
	if len(results) == 0 {
		t.Fatal("SearchRegistry returned no results for 'knowledge search'")
	}
	if results[0].Name != "test_knowledge_search" {
		t.Errorf("expected test_knowledge_search to rank first, got %q", results[0].Name)
	}
}

// --- GetCompactDirectory tests ---

func TestGetCompactDirectory(t *testing.T) {
	dir := GetCompactDirectory()
	if dir == "" {
		t.Fatal("GetCompactDirectory() returned empty string with test tools registered")
	}
	checks := []struct {
		desc    string
		contain string
	}{
		{"knowledge section header", "## knowledge"},
		{"task section header", "## task"},
		{"tool name present", "test_knowledge_search"},
	}
	for _, c := range checks {
		if !strings.Contains(dir, c.contain) {
			t.Errorf("GetCompactDirectory() missing %s (%q); got:\n%s", c.desc, c.contain, dir)
		}
	}
}

// --- GetCompactDirectoryByCategory tests ---

func TestGetCompactDirectoryByCategoryKnowledge(t *testing.T) {
	dir := GetCompactDirectoryByCategory("knowledge")
	if !strings.Contains(dir, "test_knowledge_search") {
		t.Errorf("GetCompactDirectoryByCategory('knowledge') missing test_knowledge_search; got:\n%s", dir)
	}
	if strings.Contains(dir, "## task") {
		t.Error("GetCompactDirectoryByCategory('knowledge') should not contain '## task'")
	}
}

func TestGetCompactDirectoryByCategoryNonexistent(t *testing.T) {
	dir := GetCompactDirectoryByCategory("nonexistent")
	if dir != "" {
		t.Errorf("GetCompactDirectoryByCategory('nonexistent') = %q, want ''", dir)
	}
}

func TestGetCompactDirectoryByCategoryHeader(t *testing.T) {
	dir := GetCompactDirectoryByCategory("knowledge")
	if !strings.HasPrefix(dir, "## knowledge") {
		t.Errorf("GetCompactDirectoryByCategory('knowledge') should start with '## knowledge'; got:\n%s", dir)
	}
}

// --- FormatToolsForDiscovery tests ---

func TestFormatToolsForDiscoveryEmpty(t *testing.T) {
	msg := FormatToolsForDiscovery(nil)
	if !strings.Contains(msg, "No matching tools found") {
		t.Errorf("FormatToolsForDiscovery(nil) = %q, want 'No matching tools found...'", msg)
	}
}

func TestFormatToolsForDiscoveryNonEmpty(t *testing.T) {
	summaries := []ToolSummary{
		{Name: "test_knowledge_search", Description: "Search knowledge base.", ParamNames: []string{"query"}},
	}
	msg := FormatToolsForDiscovery(summaries)
	if !strings.Contains(msg, "Available tools:") {
		t.Errorf("FormatToolsForDiscovery missing 'Available tools:'; got:\n%s", msg)
	}
	if !strings.Contains(msg, "test_knowledge_search") {
		t.Errorf("FormatToolsForDiscovery missing tool name; got:\n%s", msg)
	}
}

func TestFormatToolsForDiscoveryInvokeInstruction(t *testing.T) {
	summaries := []ToolSummary{
		{Name: "test_web_fetch", Description: "Fetch a URL.", ParamNames: []string{"query"}},
	}
	msg := FormatToolsForDiscovery(summaries)
	if !strings.Contains(msg, "fenced JSON block") {
		t.Errorf("FormatToolsForDiscovery missing invoke instruction; got:\n%s", msg)
	}
}

// --- FormatDiscoveryResultFull tests ---

func TestFormatDiscoveryResultFullEmpty(t *testing.T) {
	msg := FormatDiscoveryResultFull(nil)
	if !strings.Contains(msg, "No matching tools found") {
		t.Errorf("FormatDiscoveryResultFull(nil) = %q, want 'No matching tools found...'", msg)
	}
}

func TestFormatDiscoveryResultFullNonEmpty(t *testing.T) {
	tool := GetTool("test_knowledge_search")
	if tool == nil {
		t.Fatal("test_knowledge_search not found in registry")
	}
	msg := FormatDiscoveryResultFull([]*Tool{tool})
	if !strings.Contains(msg, "Tool schemas") {
		t.Errorf("FormatDiscoveryResultFull missing 'Tool schemas'; got:\n%s", msg)
	}
	if !strings.Contains(msg, "test_knowledge_search") {
		t.Errorf("FormatDiscoveryResultFull missing tool name; got:\n%s", msg)
	}
	if !strings.Contains(msg, "query") {
		t.Errorf("FormatDiscoveryResultFull missing param description; got:\n%s", msg)
	}
}
