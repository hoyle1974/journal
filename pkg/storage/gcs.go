package storage

import (
	"context"
	"fmt"
	"path"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
)

const (
	gcsMaxSize     = 10 << 20 // 10MB
	objectPrefix   = "images/"
	contentTypeJPG = "image/jpeg"
	contentTypePNG = "image/png"
	contentTypeWEBP = "image/webp"
	contentTypeGIF = "image/gif"
)

// GCSImageStorage uploads images to a Google Cloud Storage bucket.
type GCSImageStorage struct {
	client *storage.Client
	bucket string
}

// NewGCSImageStorage returns a GCS-backed ImageStorage. bucket must be non-empty.
func NewGCSImageStorage(client *storage.Client, bucket string) *GCSImageStorage {
	return &GCSImageStorage{client: client, bucket: bucket}
}

// UploadImage writes data to GCS under objects/images/<uuid> and returns gs://bucket/images/<uuid>.
func (g *GCSImageStorage) UploadImage(ctx context.Context, data []byte) (string, error) {
	if g.client == nil || g.bucket == "" {
		return "", fmt.Errorf("image upload not configured: GCS client or bucket missing")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("image data is empty")
	}
	if len(data) > gcsMaxSize {
		return "", fmt.Errorf("image exceeds max size (%d bytes)", gcsMaxSize)
	}
	objName := path.Join(objectPrefix, uuid.New().String())
	w := g.client.Bucket(g.bucket).Object(objName).NewWriter(ctx)
	w.ContentType = contentTypeFromBytes(data)
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return "", fmt.Errorf("write image to GCS: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("close GCS writer: %w", err)
	}
	return fmt.Sprintf("gs://%s/%s", g.bucket, objName), nil
}

func contentTypeFromBytes(data []byte) string {
	if len(data) < 12 {
		return "application/octet-stream"
	}
	// JPEG
	if data[0] == 0xFF && data[1] == 0xD8 {
		return contentTypeJPG
	}
	// PNG
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return contentTypePNG
	}
	// GIF
	if data[0] == 'G' && data[1] == 'I' && data[2] == 'F' {
		return contentTypeGIF
	}
	// WebP (RIFF....WEBP)
	if len(data) >= 12 && data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P' {
		return contentTypeWEBP
	}
	return "application/octet-stream"
}

// noopImageStorage implements ImageStorage but does nothing; used when bucket is not set.
type noopImageStorage struct{}

func (n *noopImageStorage) UploadImage(ctx context.Context, data []byte) (string, error) {
	_ = ctx
	_ = data
	return "", fmt.Errorf("image upload not configured: set JOT_IMAGES_BUCKET to enable")
}

// NoopImageStorage returns an ImageStorage that always returns a "not configured" error.
func NoopImageStorage() ImageStorage {
	return &noopImageStorage{}
}
