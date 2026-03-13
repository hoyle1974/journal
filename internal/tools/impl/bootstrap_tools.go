package impl

import (
	"context"

	"github.com/jackstrohm/jot/tools"
)

func init() {
	registerBootstrapTools()
}

func registerBootstrapTools() {
	tools.Register(&tools.Tool{
		Name:        "discovery_search",
		Description: "Get tool schemas for an intent. Call this when you need to do something that is not semantic_search, upsert_knowledge, or discovery_search. Pass your reasoning as intent (e.g. 'scheduling a commitment', 'store a fact', 'create task'). Returns full parameter schemas for matching tools; then invoke one with key/value lines: TOOL: name then ARGS: followed by param_name | value per line. No JSON, no code fences.",
		Category:    "bootstrap",
		Params: []tools.Param{
			tools.RequiredStringParam("intent", "Your reasoning or goal in natural language (e.g. 'scheduling a commitment', 'store a fact', 'create task', 'search journal')"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			intent, ok := args.RequiredString("intent")
			if !ok {
				return tools.MissingParam("intent")
			}
			matches := tools.SearchRegistryTools(intent, 8)
			return tools.OK("%s", tools.FormatDiscoveryResultFull(matches))
		},
	})
}
