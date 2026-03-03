package tools

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/generative-ai-go/genai"
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

// Execute runs a tool by name with the given arguments.
func Execute(ctx context.Context, name string, arguments map[string]interface{}) Result {
	registryLock.RLock()
	tool, exists := registry[name]
	registryLock.RUnlock()

	if !exists {
		return Fail("Unknown tool: %s", name)
	}

	args := NewArgs(arguments)
	return tool.Execute(ctx, args)
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

	return &genai.FunctionDeclaration{
		Name:        tool.Name,
		Description: tool.Description,
		Parameters: &genai.Schema{
			Type:       genai.TypeObject,
			Properties: properties,
			Required:   required,
		},
	}
}
