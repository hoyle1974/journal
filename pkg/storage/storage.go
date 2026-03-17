package storage

import "context"

// ImageStorage uploads and downloads image bytes to/from a stable URI (e.g. gs://bucket/path).
// Implementations may use GCS or other blob storage.
type ImageStorage interface {
	// UploadImage writes image bytes to storage and returns the URI.
	// Returns an error if storage is not configured or upload fails.
	UploadImage(ctx context.Context, data []byte) (uri string, err error)

	// DownloadImage fetches image bytes from a URI previously returned by UploadImage.
	// Returns the raw bytes and detected MIME type.
	DownloadImage(ctx context.Context, uri string) (data []byte, mimeType string, err error)
}
