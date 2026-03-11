package api

import (
	"context"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/infra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// dreamRunStateApp is the minimal interface needed to read/write dream run state (avoids coupling to full AppLike).
type dreamRunStateApp interface {
	Firestore(ctx context.Context) (*firestore.Client, error)
}

// DreamRunStatus is the status of the current dream run.
const (
	DreamRunStatusPending   = "pending"
	DreamRunStatusRunning   = "running"
	DreamRunStatusCompleted = "completed"
	DreamRunStatusFailed    = "failed"
)

// DreamRunStaleThreshold is how old a run must be (no completed_at) before we allow a new one (avoids stuck "running" forever).
const DreamRunStaleThreshold = 2 * time.Hour

// DreamRunState is the shape of _system/dream_run for polling and display.
type DreamRunState struct {
	DreamRunID     string                 `json:"dream_run_id"`
	Status         string                 `json:"status"`
	CurrentPhase   string                 `json:"current_phase"`
	StartedAt      time.Time              `json:"started_at"`
	CompletedAt    *time.Time             `json:"completed_at,omitempty"`
	Error          string                 `json:"error,omitempty"`
	Log            []string               `json:"log"`
	Result         map[string]interface{} `json:"result,omitempty"`
	AlreadyRunning bool                   `json:"already_running,omitempty"` // true when POST /dream returned 202 because a run was already in progress
}

// GetDreamRunState reads the current dream run state from Firestore for polling.
func GetDreamRunState(ctx context.Context, app dreamRunStateApp) (*DreamRunState, error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := client.Collection(infra.SystemCollection).Doc(infra.DreamRunDoc).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil // doc never created yet
		}
		return nil, err
	}
	if !doc.Exists() {
		return nil, nil
	}
	return dreamRunStateFromDoc(doc), nil
}

func dreamRunStateFromDoc(doc *firestore.DocumentSnapshot) *DreamRunState {
	data := doc.Data()
	s := &DreamRunState{}
	if v, ok := data["dream_run_id"].(string); ok {
		s.DreamRunID = v
	}
	if v, ok := data["status"].(string); ok {
		s.Status = v
	}
	if v, ok := data["current_phase"].(string); ok {
		s.CurrentPhase = v
	}
	if v, ok := data["started_at"].(time.Time); ok {
		s.StartedAt = v
	}
	if v, ok := data["completed_at"].(time.Time); ok {
		s.CompletedAt = &v
	}
	if v, ok := data["error"].(string); ok {
		s.Error = v
	}
	if v, ok := data["log"].([]interface{}); ok {
		for _, e := range v {
			if msg, ok := e.(string); ok {
				s.Log = append(s.Log, msg)
			}
		}
	}
	if v, ok := data["result"].(map[string]interface{}); ok {
		s.Result = v
	}
	return s
}

// TryAcquireDreamRunLock sets status to pending and started_at if no run is in progress or the existing run is stale.
// Returns (acquired, existingRunID). When acquired is false, existingRunID is the in-progress run's ID for the 202 response.
func TryAcquireDreamRunLock(ctx context.Context, app dreamRunStateApp, runID string) (acquired bool, existingRunID string, err error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return false, "", err
	}
	ref := client.Collection(infra.SystemCollection).Doc(infra.DreamRunDoc)
	if err := ensureDreamRunDocExists(ctx, client, ref); err != nil {
		return false, "", err
	}
	var existingID string
	acquireFn := func(doc *firestore.DocumentSnapshot, tx *firestore.Transaction) (map[string]interface{}, error) {
		now := time.Now()
		if doc != nil && doc.Exists() {
			existing := doc.Data()
			st, _ := existing["status"].(string)
			startedAt, _ := existing["started_at"].(time.Time)
			if id, ok := existing["dream_run_id"].(string); ok {
				existingID = id
			}
			if st == DreamRunStatusRunning || st == DreamRunStatusPending {
				if time.Since(startedAt) < DreamRunStaleThreshold {
					return nil, nil // cannot acquire
				}
			}
		}
		return map[string]interface{}{
			"dream_run_id":  runID,
			"status":        DreamRunStatusPending,
			"current_phase": "pending",
			"started_at":    now,
			"log":           []string{"Dream run queued."},
			"completed_at":  nil,
			"error":         "",
			"result":        nil,
		}, nil
	}
	committed, err := runTransaction(ctx, client, ref, acquireFn)
	if err != nil && status.Code(err) == codes.NotFound {
		// Doc still missing (e.g. eventual consistency); ensure again and retry once.
		_ = ensureDreamRunDocExists(ctx, client, ref)
		committed, err = runTransaction(ctx, client, ref, acquireFn)
	}
	if err != nil {
		return false, "", err
	}
	if !committed {
		return false, existingID, nil
	}
	return true, "", nil
}

// ensureDreamRunDocExists creates _system/dream_run with a completed placeholder if it does not exist,
// so the transaction in TryAcquireDreamRunLock never does Get on a missing doc (which can abort with NotFound).
func ensureDreamRunDocExists(ctx context.Context, client *firestore.Client, ref *firestore.DocumentRef) error {
	placeholder := map[string]interface{}{
		"status":        DreamRunStatusCompleted,
		"dream_run_id":  "",
		"current_phase": "complete",
		"completed_at":  time.Now().Add(-DreamRunStaleThreshold), // old so next run can acquire
		"log":           []string{},
		"error":         "",
		"result":        nil,
	}
	doc, err := ref.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			_, err = ref.Set(ctx, placeholder, firestore.MergeAll)
			return err
		}
		return err
	}
	if !doc.Exists() {
		_, err = ref.Set(ctx, placeholder, firestore.MergeAll)
		return err
	}
	return nil
}

func runTransaction(ctx context.Context, client *firestore.Client, ref *firestore.DocumentRef, fn func(*firestore.DocumentSnapshot, *firestore.Transaction) (map[string]interface{}, error)) (committed bool, err error) {
	err = client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if err != nil {
			return err
		}
		updates, err := fn(doc, tx)
		if err != nil {
			return err
		}
		if updates == nil {
			return nil // caller chose not to update (e.g. lock not acquired)
		}
		committed = true
		return tx.Set(ref, updates, firestore.MergeAll)
	})
	return committed, err
}

// UpdateDreamRunPhase updates current_phase and appends a log line.
func UpdateDreamRunPhase(ctx context.Context, app dreamRunStateApp, runID string, phase string, logLine string) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	ref := client.Collection(infra.SystemCollection).Doc(infra.DreamRunDoc)
	updates := []firestore.Update{
		{Path: "status", Value: DreamRunStatusRunning},
		{Path: "current_phase", Value: phase},
	}
	if logLine != "" {
		updates = append(updates, firestore.Update{Path: "log", Value: firestore.ArrayUnion(logLine)})
	}
	_, err = ref.Update(ctx, updates)
	return err
}

// SetDreamRunCompleted sets status to completed, result, and completed_at.
func SetDreamRunCompleted(ctx context.Context, app dreamRunStateApp, runID string, result map[string]interface{}) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	_, err = client.Collection(infra.SystemCollection).Doc(infra.DreamRunDoc).Update(ctx, []firestore.Update{
		{Path: "status", Value: DreamRunStatusCompleted},
		{Path: "current_phase", Value: "complete"},
		{Path: "completed_at", Value: now},
		{Path: "result", Value: result},
		{Path: "log", Value: firestore.ArrayUnion("Dream run completed.")},
	})
	return err
}

// SetDreamRunFailed sets status to failed, error message, and completed_at.
func SetDreamRunFailed(ctx context.Context, app dreamRunStateApp, runID string, errMsg string) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	_, err = client.Collection(infra.SystemCollection).Doc(infra.DreamRunDoc).Update(ctx, []firestore.Update{
		{Path: "status", Value: DreamRunStatusFailed},
		{Path: "current_phase", Value: "failed"},
		{Path: "completed_at", Value: now},
		{Path: "error", Value: errMsg},
		{Path: "log", Value: firestore.ArrayUnion("Dream run failed: " + errMsg)},
	})
	return err
}

// AppendDreamRunLog appends a line to the log without changing phase.
func AppendDreamRunLog(ctx context.Context, app dreamRunStateApp, runID string, logLine string) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(infra.SystemCollection).Doc(infra.DreamRunDoc).Update(ctx, []firestore.Update{
		{Path: "log", Value: firestore.ArrayUnion(logLine)},
	})
	return err
}
