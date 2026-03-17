// Package tools provides a struct-based tool registration system for the jot agent.
// Each tool co-locates its definition and implementation for better maintainability.
// Tool schema (JSON Schema for Gemini) is generated from the Args struct via reflection.
package tools

import (
	"context"

	"github.com/jackstrohm/jot/internal/infra"
)

// Result represents the result of executing a tool.
// This type is defined here but is compatible with jot.ToolResult.
type Result struct {
	Success bool
	Result  string
}

// ExecuteFunc is the function signature for tool execution.
// args is the same type as Tool.Args (e.g. *WeatherArgs); implementers type-assert.
// env may be nil when running tools that do not need it (e.g. in tests).
type ExecuteFunc func(ctx context.Context, env infra.ToolEnv, args any) Result

// Tool defines a tool that can be called by the LLM agent.
// Args must be a pointer to a struct with json and description struct tags; the schema is derived via reflection.
// DocURL is optional; when set, it is included in the tool description sent to the LLM.
type Tool struct {
	Name        string
	Description string
	Category    string
	Args        any          // pointer to struct (e.g. &WeatherArgs{}); use NoArgs{} for tools with no parameters
	Execute     ExecuteFunc
	DocURL      string       // Optional link to library or API docs (e.g. GitHub or pkg.go.dev).
}

// NoArgs is used for tools that take no parameters. Use Args: &NoArgs{} and in Execute ignore args.
type NoArgs struct{}
