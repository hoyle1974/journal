package jot

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/google/uuid"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// WrapFirestoreIndexError wraps Firestore "query requires an index" errors with a user-facing
// message and deploy instructions. The console link in the raw error often does not work.
func WrapFirestoreIndexError(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) != codes.FailedPrecondition {
		return err
	}
	if !strings.Contains(err.Error(), "index") {
		return err
	}
	return fmt.Errorf("Firestore query requires a composite index. Add the needed index to firestore.indexes.json and run: firebase deploy --only firestore:indexes — %w", err)
}

// QueryDocuments runs a Firestore query and maps each document with mapDoc. Skips documents for which mapDoc returns an error.
func QueryDocuments[T any](ctx context.Context, query firestore.Query, mapDoc func(*firestore.DocumentSnapshot) (T, error)) ([]T, error) {
	iter := query.Documents(ctx)
	defer iter.Stop()
	var results []T
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		t, err := mapDoc(doc)
		if err != nil {
			continue
		}
		results = append(results, t)
	}
	return results, nil
}

// GetFirestoreClient returns the Firestore client from the App in context.
// Callers must use a context that has App attached (e.g. from an HTTP request).
// For non-HTTP code (e.g. CLI tools), create an App with NewApp and attach with WithApp.
func GetFirestoreClient(ctx context.Context) (*firestore.Client, error) {
	app := GetApp(ctx)
	if app == nil {
		return nil, fmt.Errorf("no app in context")
	}
	return app.Firestore(ctx)
}

// GenerateUUID creates a new UUID for entries/todos.
func GenerateUUID() string {
	return uuid.New().String()
}

// getStringField returns a string field from Firestore document data.
func getStringField(data map[string]interface{}, field string) string {
	if v, ok := data[field].(string); ok {
		return v
	}
	return ""
}

// getStringSliceField parses a Firestore array of strings (or interface{} elements) into []string.
func getStringSliceField(data map[string]interface{}, field string) []string {
	v, ok := data[field].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(v))
	for _, e := range v {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
