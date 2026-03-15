package system

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/infra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	SyncLockDocument = "sync_lock"
	SyncStateDocument = "sync_state"
	SyncDebounceDocument = "sync_debounce"
	SyncLockTimeout = 15 * time.Minute
)

// AcquireSyncLock acquires the sync lock in _system/sync_lock. Returns true if acquired, false if already held.
func AcquireSyncLock(ctx context.Context, app FirestoreProvider) (bool, error) {
	client, err := app.Firestore(ctx)
	if err != nil || client == nil {
		return true, nil
	}
	lockRef := client.Collection(infra.SystemCollection).Doc(SyncLockDocument)
	err = client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(lockRef)
		now := time.Now()
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return tx.Set(lockRef, map[string]interface{}{
					"locked_at": now,
					"locked_by": "sync",
				})
			}
			return err
		}
		if lockedAt, ok := doc.Data()["locked_at"].(time.Time); ok {
			if now.Sub(lockedAt) > SyncLockTimeout {
				return tx.Set(lockRef, map[string]interface{}{
					"locked_at": now,
					"locked_by": "sync",
				})
			}
		}
		return fmt.Errorf("lock held")
	})
	if err != nil {
		if err.Error() == "lock held" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ReleaseSyncLock deletes the sync lock document.
func ReleaseSyncLock(ctx context.Context, app FirestoreProvider) {
	client, err := app.Firestore(ctx)
	if err != nil || client == nil {
		return
	}
	lockRef := client.Collection(infra.SystemCollection).Doc(SyncLockDocument)
	if _, err := lockRef.Delete(ctx); err != nil {
		infra.LoggerFrom(ctx).Error("failed to release sync lock", "error", err)
	}
}

// GetSyncStateLastBlockHash returns the last processed block hash from _system/sync_state, if any.
func GetSyncStateLastBlockHash(ctx context.Context, app FirestoreProvider) (hash string, exists bool, err error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return "", false, err
	}
	if client == nil {
		return "", false, nil
	}
	stateRef := client.Collection(infra.SystemCollection).Doc(SyncStateDocument)
	doc, err := stateRef.Get(ctx)
	if err != nil || !doc.Exists() {
		return "", false, err
	}
	if v, ok := doc.Data()["last_block_hash"].(string); ok {
		return v, true, nil
	}
	return "", true, nil
}

// SetSyncStateAfterProcess writes last_block_hash and last_processed_at to _system/sync_state.
func SetSyncStateAfterProcess(ctx context.Context, app FirestoreProvider, blockHash string) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	if client == nil {
		return nil
	}
	_, err = client.Collection(infra.SystemCollection).Doc(SyncStateDocument).Set(ctx, map[string]interface{}{
		"last_block_hash":   blockHash,
		"last_processed_at": time.Now(),
	})
	return err
}

// GetDebounceState returns the current task_name from _system/sync_debounce, if any.
func GetDebounceState(ctx context.Context, app FirestoreProvider) (taskName string, err error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return "", err
	}
	ref := client.Collection(infra.SystemCollection).Doc(SyncDebounceDocument)
	doc, err := ref.Get(ctx)
	if err != nil || !doc.Exists() {
		return "", err
	}
	if v, ok := doc.Data()["task_name"].(string); ok {
		return v, nil
	}
	return "", nil
}

// SetDebounceState writes task_name and scheduled_time to _system/sync_debounce.
func SetDebounceState(ctx context.Context, app FirestoreProvider, taskName string, scheduledTime time.Time) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(infra.SystemCollection).Doc(SyncDebounceDocument).Set(ctx, map[string]interface{}{
		"task_name":      taskName,
		"scheduled_time": scheduledTime.Format(time.RFC3339),
	})
	return err
}
