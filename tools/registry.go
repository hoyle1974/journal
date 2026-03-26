package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jackstrohm/jot/internal/infra"
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
	if arguments == nil {
		arguments = make(map[string]interface{})
	}

	typedArgs, err := MapToTypedArgs(tool, arguments)
	if err != nil {
		return Fail("Invalid arguments: %v", err)
	}
	if typedArgs != nil {
		ApplyDefaults(typedArgs)
	}
	return tool.Execute(ctx, env, typedArgs)
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

// toolToDeclaration converts a Tool to a genai.FunctionDeclaration using reflection on Tool.Args.
func toolToDeclaration(tool *Tool) *genai.FunctionDeclaration {
	paramsSchema := StructToGenaiSchema(tool.Args)
	desc := tool.Description
	if tool.DocURL != "" {
		desc = desc + "\nDocs: " + tool.DocURL
	}
	return &genai.FunctionDeclaration{
		Name:        tool.Name,
		Description: desc,
		Parameters:  paramsSchema,
	}
}
