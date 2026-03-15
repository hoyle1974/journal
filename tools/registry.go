package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackstrohm/jot/pkg/infra"
	"google.golang.org/genai"
)

var (
	registry     = make(map[string]*Tool)
	registryLock sync.RWMutex
)

// Register adds a tool to the global registry.
// Called from init() functions in domain files.
func Register(tool *Tool) {
	registryLock.Lock()
	defer registryLock.Unlock()

	if _, exists := registry[tool.Name]; exists {
		panic(fmt.Sprintf("tool already registered: %s", tool.Name))
	}
	registry[tool.Name] = tool
}

// GetDefinitions returns all registered tools as genai.FunctionDeclarations.
func GetDefinitions() []*genai.FunctionDeclaration {
	registryLock.RLock()
	defer registryLock.RUnlock()

	definitions := make([]*genai.FunctionDeclaration, 0, len(registry))
	for _, tool := range registry {
		definitions = append(definitions, toolToDeclaration(tool))
	}
	return definitions
}

// GetDefinitionsByCategory returns function declarations for tools in the given category (e.g. "task").
func GetDefinitionsByCategory(category string) []*genai.FunctionDeclaration {
	registryLock.RLock()
	defer registryLock.RUnlock()

	var definitions []*genai.FunctionDeclaration
	for _, tool := range registry {
		if tool.Category == category {
			definitions = append(definitions, toolToDeclaration(tool))
		}
	}
	return definitions
}

// Execute runs a tool by name with the given arguments. env is passed explicitly so tools do not pull app from context; may be nil for tools that do not need it.
func Execute(ctx context.Context, env infra.ToolEnv, name string, arguments map[string]interface{}) Result {
	registryLock.RLock()
	tool, exists := registry[name]
	registryLock.RUnlock()

	if !exists {
		return Fail("Unknown tool: %s", name)
	}

	args := NewArgs(arguments)
	return tool.Execute(ctx, env, args)
}

// GetTool returns a tool by name (for testing).
func GetTool(name string) *Tool {
	registryLock.RLock()
	defer registryLock.RUnlock()
	return registry[name]
}

// Count returns the number of registered tools.
func Count() int {
	registryLock.RLock()
	defer registryLock.RUnlock()
	return len(registry)
}

// GetCompactDirectory returns a minimal tool directory (name + one-line description per tool, grouped by category)
// for use in prompts when not sending full FunctionDeclarations (MCP-style: full schema stays server-side).
func GetCompactDirectory() string {
	registryLock.RLock()
	defer registryLock.RUnlock()

	byCat := make(map[string][]*Tool)
	for _, t := range registry {
		byCat[t.Category] = append(byCat[t.Category], t)
	}
	cats := []string{"knowledge", "journal", "context", "task", "query", "web", "utility", "specialist"}
	used := make(map[string]bool)
	for _, c := range cats {
		used[c] = true
	}
	var lines []string
	for _, cat := range cats {
		tlist := byCat[cat]
		if len(tlist) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("\n## %s", cat))
		for _, t := range tlist {
			short := firstSentence(t.Description, 80)
			lines = append(lines, fmt.Sprintf("- %s: %s", t.Name, short))
		}
	}
	for cat, tlist := range byCat {
		if used[cat] {
			continue
		}
		lines = append(lines, fmt.Sprintf("\n## %s", cat))
		for _, t := range tlist {
			lines = append(lines, fmt.Sprintf("- %s: %s", t.Name, firstSentence(t.Description, 80)))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ToolSummary is a minimal tool descriptor for MCP-style discovery (search_tools result).
type ToolSummary struct {
	Name        string   // tool name to use in invoke
	Description string   // one-line description
	ParamNames  []string // param names for hint (e.g. "content", "node_type", "metadata?")
}

const discoverySearchName = "discovery_search"

// CoreToolNames are the 3 tools always loaded in "Map vs Manual" mode (semantic_search, upsert_knowledge, discovery_search).
var CoreToolNames = []string{"semantic_search", "upsert_knowledge", discoverySearchName}

// GetDefinitionsForCore returns FunctionDeclarations for the core tools only (~300 tokens). Used when UseCompactTools: agent gets the Map + these 3; everything else via discovery_search.
func GetDefinitionsForCore() []*genai.FunctionDeclaration {
	registryLock.RLock()
	defer registryLock.RUnlock()

	out := make([]*genai.FunctionDeclaration, 0, len(CoreToolNames))
	for _, name := range CoreToolNames {
		if t, ok := registry[name]; ok {
			out = append(out, toolToDeclaration(t))
		}
	}
	return out
}

// SearchRegistry returns tools matching the intent (keyword match on name, description, category).
// Excludes the bootstrap tool discovery_search. Used by discovery_search Execute.
func SearchRegistry(query string, limit int) []ToolSummary {
	registryLock.RLock()
	defer registryLock.RUnlock()

	query = strings.ToLower(strings.TrimSpace(query))
	words := strings.Fields(query)
	if len(words) == 0 {
		words = []string{query}
	}

	type scored struct {
		t    *Tool
		score int
	}
	var scoredList []scored
	for _, t := range registry {
		if t.Name == discoverySearchName {
			continue
		}
		text := strings.ToLower(t.Name + " " + t.Category + " " + t.Description)
		score := 0
		for _, w := range words {
			if len(w) < 2 {
				continue
			}
			if strings.Contains(text, w) {
				score++
			}
			if strings.HasPrefix(t.Name, w) || strings.Contains(t.Name, w) {
				score += 2
			}
		}
		if score > 0 {
			scoredList = append(scoredList, scored{t, score})
		}
	}
	// Sort by score descending (simple bubble or we need sort.Slice)
	for i := 0; i < len(scoredList); i++ {
		for j := i + 1; j < len(scoredList); j++ {
			if scoredList[j].score > scoredList[i].score {
				scoredList[i], scoredList[j] = scoredList[j], scoredList[i]
			}
		}
	}
	if limit <= 0 {
		limit = 8
	}
	if len(scoredList) > limit {
		scoredList = scoredList[:limit]
	}
	out := make([]ToolSummary, 0, len(scoredList))
	for _, s := range scoredList {
		paramNames := make([]string, 0, len(s.t.Params))
		for _, p := range s.t.Params {
			name := p.Name
			if !p.Required {
				name = name + "?"
			}
			paramNames = append(paramNames, name)
		}
		out = append(out, ToolSummary{
			Name:        s.t.Name,
			Description: firstSentence(s.t.Description, 80),
			ParamNames:  paramNames,
		})
	}
	return out
}

// SearchRegistryTools returns matching *Tool pointers (excluding discovery_search). Use for JIT schema injection.
func SearchRegistryTools(query string, limit int) []*Tool {
	registryLock.RLock()
	defer registryLock.RUnlock()

	query = strings.ToLower(strings.TrimSpace(query))
	words := strings.Fields(query)
	if len(words) == 0 {
		words = []string{query}
	}
	type scored struct {
		t    *Tool
		score int
	}
	var scoredList []scored
	for _, t := range registry {
		if t.Name == discoverySearchName {
			continue
		}
		text := strings.ToLower(t.Name + " " + t.Category + " " + t.Description)
		score := 0
		for _, w := range words {
			if len(w) < 2 {
				continue
			}
			if strings.Contains(text, w) {
				score++
			}
			if strings.HasPrefix(t.Name, w) || strings.Contains(t.Name, w) {
				score += 2
			}
		}
		if score > 0 {
			scoredList = append(scoredList, scored{t, score})
		}
	}
	for i := 0; i < len(scoredList); i++ {
		for j := i + 1; j < len(scoredList); j++ {
			if scoredList[j].score > scoredList[i].score {
				scoredList[i], scoredList[j] = scoredList[j], scoredList[i]
			}
		}
	}
	if limit <= 0 {
		limit = 8
	}
	if len(scoredList) > limit {
		scoredList = scoredList[:limit]
	}
	out := make([]*Tool, 0, len(scoredList))
	for _, s := range scoredList {
		out = append(out, s.t)
	}
	return out
}

// FormatDiscoveryResultFull formats tools with full param schemas for JIT injection (Map vs Manual). Model sees exactly how to call each tool.
func FormatDiscoveryResultFull(tools []*Tool) string {
	if len(tools) == 0 {
		return "No matching tools found. Try a different intent (e.g. \"search journal\", \"create task\", \"store fact\")."
	}
	var lines []string
	lines = append(lines, "Tool schemas (invoke with a JSON block: {\"tool\": \"name\", \"args\": {...}}):")
	for _, t := range tools {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("**%s**: %s", t.Name, firstSentence(t.Description, 120)))
		for _, p := range t.Params {
			req := "optional"
			if p.Required {
				req = "required"
			}
			lines = append(lines, fmt.Sprintf("  - %s (%s): %s", p.Name, req, p.Description))
		}
	}
	lines = append(lines, "")
	lines = append(lines, "To invoke one of these tools, respond with ONLY a fenced JSON block (```json ... ```): {\"tool\": \"tool_name\", \"args\": {\"param\": \"value\", ...}}.")
	return strings.Join(lines, "\n")
}

// FormatToolsForDiscovery formats SearchRegistry results for the model and appends the invoke instruction.
func FormatToolsForDiscovery(summaries []ToolSummary) string {
	if len(summaries) == 0 {
		return "No matching tools found. Try a different query (e.g. \"search journal\", \"create task\", \"store fact\")."
	}
	var lines []string
	lines = append(lines, "Available tools:")
	for _, s := range summaries {
		paramStr := strings.Join(s.ParamNames, ", ")
		lines = append(lines, fmt.Sprintf("- %s(%s): %s", s.Name, paramStr, s.Description))
	}
	lines = append(lines, "")
	lines = append(lines, "To invoke a tool, respond with ONLY a fenced JSON block (```json ... ```): {\"tool\": \"tool_name\", \"args\": {\"param\": \"value\", ...}}.")
	return strings.Join(lines, "\n")
}

// GetCompactDirectoryByCategory returns a minimal tool directory for a single category (e.g. "task").
// Used when only a subset of tools is available to the agent (e.g. dreamer task phase).
func GetCompactDirectoryByCategory(category string) string {
	registryLock.RLock()
	defer registryLock.RUnlock()

	var lines []string
	for _, t := range registry {
		if t.Category != category {
			continue
		}
		short := firstSentence(t.Description, 80)
		lines = append(lines, fmt.Sprintf("- %s: %s", t.Name, short))
	}
	if len(lines) == 0 {
		return ""
	}
	return "## " + category + "\n" + strings.Join(lines, "\n")
}

func firstSentence(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	for i, r := range s {
		if r == '.' || r == '\n' {
			out := strings.TrimSpace(s[:i+1])
			if len(out) > maxLen {
				return out[:maxLen] + "..."
			}
			return out
		}
		if i+1 >= maxLen {
			return s[:maxLen] + "..."
		}
	}
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// toolToDeclaration converts a Tool to a genai.FunctionDeclaration.
func toolToDeclaration(tool *Tool) *genai.FunctionDeclaration {
	properties := make(map[string]*genai.Schema)
	var required []string

	for _, param := range tool.Params {
		schema := &genai.Schema{
			Type:        param.Type,
			Description: param.Description,
		}
		if len(param.Enum) > 0 {
			schema.Enum = param.Enum
		}
		properties[param.Name] = schema

		if param.Required {
			required = append(required, param.Name)
		}
	}

	desc := tool.Description
	if tool.DocURL != "" {
		desc = desc + "\nDocs: " + tool.DocURL
	}
	return &genai.FunctionDeclaration{
		Name:        tool.Name,
		Description: desc,
		Parameters: &genai.Schema{
			Type:       genai.TypeObject,
			Properties: properties,
			Required:   required,
		},
	}
}
