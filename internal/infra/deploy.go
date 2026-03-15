package infra

import (
	"context"
	"time"

	"cloud.google.com/go/firestore"
)

// RunOnNewDeploy checks whether the running binary's Commit is newer than the commit stored in
// Firestore (_system/deploy_meta). If so, it runs the given action once and then updates the
// stored commit. Only one instance (the first to write) runs the action; others see the updated
// commit and skip. Use this to trigger one-time logic when a new revision goes live (e.g. clear
// caches, send a notification). Call after the app and Firestore are initialized (e.g. in init or
// on first request).
func RunOnNewDeploy(ctx context.Context, client *firestore.Client, action func()) error {
	ref := client.Collection(SystemCollection).Doc(DeployMetaDoc)
	var shouldRun bool
	err := client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if err != nil {
			return err
		}
		var stored string
		if doc.Exists() {
			if v, ok := doc.Data()["commit"].(string); ok {
				stored = v
			}
		}
		if stored == Commit {
			shouldRun = false
			return nil
		}
		shouldRun = true
		return tx.Set(ref, map[string]interface{}{
			"commit":     Commit,
			"version":    Version,
			"updated_at": time.Now().Format(time.RFC3339),
		})
	})
	if err != nil {
		return err
	}
	if shouldRun {
		action()
	}
	return nil
}
