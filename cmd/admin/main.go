// admin runs administrative subcommands: clean-test, export-journal, graph-query, replay-journal, reset-firestore, show-logged, show-processed, strip-done.
// Usage: go run ./cmd/admin <subcommand> [flags]
//   clean-test       - delete entries by source (-source=required [-dry-run])
//   export-journal   - export all journal entries to a local archive (--output=required)
//   graph-query      - search memory by keyword/phrase and print the graph up to N hops ([-depth=1] [-limit=10] [-limit-per-edge=5] <query>)
//   replay-journal   - replay a local archive to the Jot API (--archive=required [--api-url] [--api-key])
//   reset-firestore  - delete all data in Firestore (requires typing a random 3-digit code to confirm)
//   show-logged      - list journal entries whose content contains "Logged" ([-preview=120] [-remove] [-dry-run])
//   show-processed   - list journal entries whose content contains "Processed" ([-preview=120] [-remove] [-dry-run])
//   strip-done       - remove trailing "done." / "done" from journal entry content ([-dry-run])
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/infra"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <clean-test|export-journal|graph-query|replay-journal|reset-firestore|show-logged|show-processed|strip-done> [flags]\n", os.Args[0])
		os.Exit(1)
	}
	subcommand := os.Args[1]
	args := os.Args[2:]

	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	app, err := infra.NewApp(ctx, cfg, nil)
	if err != nil {
		log.Fatalf("NewApp: %v", err)
	}
	switch subcommand {
	case "clean-test":
		runCleanTest(ctx, app, args)
	case "export-journal":
		runExportJournal(ctx, app, args)
	case "graph-query":
		runGraphQuery(ctx, app, args)
	case "reset-firestore":
		runResetFirestore(ctx, app, args)
	case "show-logged":
		runShowLogged(ctx, app, args)
	case "show-processed":
		runShowProcessed(ctx, app, args)
	case "replay-journal":
		runReplayJournal(ctx, app, args)
	case "strip-done":
		runStripDone(ctx, app, args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand %q\n", subcommand)
		os.Exit(1)
	}
}
