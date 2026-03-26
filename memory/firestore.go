package memory

import (
	"context"

	"cloud.google.com/go/firestore"
	"github.com/google/uuid"
	"google.golang.org/api/iterator"
)

// queryDocuments runs a Firestore query and maps each document with mapDoc.
// Copied from internal/infra/firestore.go.
func queryDocuments[T any](ctx context.Context, query firestore.Query, mapDoc func(*firestore.DocumentSnapshot) (T, error)) ([]T, error) {
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

func generateUUID() string { return uuid.New().String() }

func getStringField(data map[string]any, field string) string {
	if v, ok := data[field].(string); ok {
		return v
	}
	return ""
}

func getStringSliceField(data map[string]any, field string) []string {
	v, ok := data[field].([]any)
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

func getFloat64Field(data map[string]any, field string) float64 {
	switch v := data[field].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func getFloat32SliceField(data map[string]any, field string) []float32 {
	v, ok := data[field].([]any)
	if !ok {
		return nil
	}
	out := make([]float32, 0, len(v))
	for _, e := range v {
		if f, ok := e.(float64); ok {
			out = append(out, float32(f))
		}
	}
	return out
}
