package system

import (
	"context"
	"errors"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/infra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const LatestDreamDocument = "latest_dream"

// LatestDream holds the latest dream narrative and metadata from _system/latest_dream.
type LatestDream struct {
	Narrative string
	Timestamp string
	Unread    bool
}

// GetLatestDream reads _system/latest_dream. Returns (nil, nil) if the doc does not exist.
func GetLatestDream(ctx context.Context, app FirestoreProvider) (*LatestDream, error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := client.Collection(infra.SystemCollection).Doc(LatestDreamDocument).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}
	if !doc.Exists() {
		return nil, nil
	}
	data := doc.Data()
	out := &LatestDream{}
	if v, ok := data["narrative"].(string); ok {
		out.Narrative = v
	}
	if v, ok := data["timestamp"].(string); ok {
		out.Timestamp = v
	}
	if v, ok := data["unread"].(bool); ok {
		out.Unread = v
	}
	return out, nil
}

// MarkLatestDreamRead sets unread to false on _system/latest_dream.
func MarkLatestDreamRead(ctx context.Context, app FirestoreProvider) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(infra.SystemCollection).Doc(LatestDreamDocument).Update(ctx, []firestore.Update{
		{Path: "unread", Value: false},
	})
	return err
}

// WriteLatestDream writes narrative, timestamp, and unread to _system/latest_dream (used by dreamer).
func WriteLatestDream(ctx context.Context, app FirestoreProvider, narrative string, timestamp string, unread bool) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(infra.SystemCollection).Doc(LatestDreamDocument).Set(ctx, map[string]interface{}{
		"narrative": narrative,
		"timestamp": timestamp,
		"unread":    unread,
	})
	return err
}

// GetLatestDreamFromContext reads _system/latest_dream using app from context (for legacy callers such as tools).
func GetLatestDreamFromContext(ctx context.Context) (*LatestDream, error) {
	app := infra.GetApp(ctx)
	if app == nil {
		return nil, errors.New("no app in context")
	}
	return GetLatestDream(ctx, app)
}
