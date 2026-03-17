package impl

import (
	"context"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/tools"
)

type discoverySearchArgs struct {
	Intent string `json:"intent" description:"Your reasoning or goal in natural language (e.g. 'scheduling a commitment', 'store a fact', 'create task', 'search journal')" required:"true"`
}

func init() {
	registerBootstrapTools()
}

func registerBootstrapTools() {
	tools.Register(&tools.Tool{
		Name:        "discovery_search",
		Description: "Get tool schemas for an intent. Call this when you need to do something that is not semantic_search, upsert_knowledge, or discovery_search. Pass your reasoning as intent (e.g. 'scheduling a commitment', 'store a fact', 'create task'). Returns full parameter schemas for matching tools; then invoke one with key/value lines: TOOL: name then ARGS: followed by param_name | value per line. No JSON, no code fences.",
		Category:    "bootstrap",
		Args:        &discoverySearchArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*discoverySearchArgs)
			if a.Intent == "" {
				return tools.MissingParam("intent")
			}
			matches := tools.SearchRegistryTools(a.Intent, 8)
			return tools.OK("%s", tools.FormatDiscoveryResultFull(matches))
		},
	})
}
