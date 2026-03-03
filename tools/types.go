// Package tools provides a struct-based tool registration system for the jot agent.
// Each tool co-locates its definition and implementation for better maintainability.
package tools

import (
	"context"

	"github.com/google/generative-ai-go/genai"
)

// Result represents the result of executing a tool.
// This type is defined here but is compatible with jot.ToolResult.
type Result struct {
	Success bool
	Result  string
}

// ExecuteFunc is the function signature for tool execution.
type ExecuteFunc func(ctx context.Context, args *Args) Result

// Tool defines a tool that can be called by the LLM agent.
type Tool struct {
	Name        string
	Description string
	Category    string
	Params      []Param
	Execute     ExecuteFunc
}

// Param defines a parameter for a tool.
type Param struct {
	Name        string
	Description string
	Type        genai.Type
	Required    bool
	Default     interface{}
	Min         *int
	Max         *int
	Enum        []string
}
