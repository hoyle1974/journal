package system

import (
	"context"

	"cloud.google.com/go/firestore"
)

// FirestoreProvider provides access to Firestore. Implemented by *infra.App and api.AppLike.
type FirestoreProvider interface {
	Firestore(ctx context.Context) (*firestore.Client, error)
}
