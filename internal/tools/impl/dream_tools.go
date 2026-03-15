package impl

import (
	"context"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/system"
	"github.com/jackstrohm/jot/tools"
)

func init() {
	registerDreamTools()
}

func registerDreamTools() {
	tools.Register(&tools.Tool{
		Name:        "get_latest_dream",
		Description: "Get the latest dream narrative: the personal 'morning readout' from the last nightly Dreamer run. Use when the user wants to discuss what the system learned overnight, what themes were noticed, what was committed to memory, open loops, or any tool/friction the Cognitive Engineer reported. Enables conversation about last night's consolidation.",
		Category:    "knowledge",
		Params:      nil,
		Execute: func(ctx context.Context, env infra.ToolEnv, args *tools.Args) tools.Result {
			latest, err := system.GetLatestDreamFromContext(ctx)
			if err != nil {
				return tools.Fail("Error getting Firestore: %v", err)
			}
			if latest == nil {
				return tools.OK("No dream narrative has been generated yet. The nightly Dreamer writes one after it runs (e.g. via cron).")
			}
			if latest.Narrative == "" {
				return tools.OK("A dream document exists but the narrative is empty.")
			}
			if latest.Timestamp != "" {
				return tools.OK("Dream from %s:\n\n%s", latest.Timestamp, latest.Narrative)
			}
			return tools.OK("%s", latest.Narrative)
		},
	})
}
