package storage

import "context"

// ImageStorage uploads image bytes and returns a stable URI (e.g. gs://bucket/path).
// Implementations may use GCS or other blob storage.
type ImageStorage interface {
	// UploadImage writes image bytes to storage and returns the URI.
	// Returns an error if storage is not configured or upload fails.
	UploadImage(ctx context.Context, data []byte) (uri string, err error)
}
