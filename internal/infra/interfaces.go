package infra

import (
	"context"

	"cloud.google.com/go/firestore"
)

// FirestoreProvider supplies a Firestore client. Implemented by *App.
// Use this narrow interface so callers depend only on Firestore access, not the full app.
type FirestoreProvider interface {
	Firestore(ctx context.Context) (*firestore.Client, error)
}
