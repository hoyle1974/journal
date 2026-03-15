package impl

import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/system"
	"github.com/jackstrohm/jot/tools"
)

// firestoreProviderFromEnv adapts infra.ToolEnv to system.FirestoreProvider.
type firestoreProviderFromEnv struct{ env infra.ToolEnv }

func (a firestoreProviderFromEnv) Firestore(ctx context.Context) (*firestore.Client, error) {
	if a.env == nil {
		return nil, fmt.Errorf("env required")
	}
	return a.env.Firestore(ctx)
}

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
			latest, err := system.GetLatestDream(ctx, firestoreProviderFromEnv{env})
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
