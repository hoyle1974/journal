package impl

import (
	"context"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/tools"
)

const latestDreamDoc = "latest_dream"

func init() {
	registerDreamTools()
}

func registerDreamTools() {
	tools.Register(&tools.Tool{
		Name:        "get_latest_dream",
		Description: "Get the latest dream narrative: the personal 'morning readout' from the last nightly Dreamer run. Use when the user wants to discuss what the system learned overnight, what themes were noticed, what was committed to memory, open loops, or any tool/friction the Cognitive Engineer reported. Enables conversation about last night's consolidation.",
		Category:    "knowledge",
		Params:      nil,
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			client, err := infra.GetFirestoreClient(ctx)
			if err != nil {
				return tools.Fail("Error getting Firestore: %v", err)
			}
			doc, err := client.Collection(infra.SystemCollection).Doc(latestDreamDoc).Get(ctx)
			if err != nil {
				return tools.Fail("Error reading latest dream: %v", err)
			}
			if !doc.Exists() {
				return tools.OK("No dream narrative has been generated yet. The nightly Dreamer writes one after it runs (e.g. via cron).")
			}
			data := doc.Data()
			narrative, _ := data["narrative"].(string)
			timestamp, _ := data["timestamp"].(string)
			if narrative == "" {
				return tools.OK("A dream document exists but the narrative is empty.")
			}
			if timestamp != "" {
				return tools.OK("Dream from %s:\n\n%s", timestamp, narrative)
			}
			return tools.OK("%s", narrative)
		},
	})
}
