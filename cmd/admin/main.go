// admin runs administrative subcommands: backfill-links, clean-test, dedup, migrate-meta, strip-done.
// Usage: go run ./cmd/admin <subcommand> [flags]
//   backfill-links  - link journal entries to knowledge nodes ([-limit=100] [-dry-run])
//   clean-test      - delete entries by source (-source=required [-dry-run])
//   dedup           - merge duplicate knowledge nodes ([-dry-run] [-threshold=0.95])
//   migrate-meta    - repair knowledge_nodes metadata ([-dry-run])
//   strip-done      - remove trailing "done." / "done" from journal entry content ([-dry-run])
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <backfill-links|clean-test|dedup|migrate-meta|strip-done> [flags]\n", os.Args[0])
		os.Exit(1)
	}
	switch os.Args[1] {
	case "backfill-links":
		runBackfillLinks()
	case "clean-test":
		runCleanTest()
	case "dedup":
		runDedup()
	case "migrate-meta":
		runMigrateMeta()
	case "strip-done":
		runStripDone()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand %q\n", os.Args[1])
		os.Exit(1)
	}
}
