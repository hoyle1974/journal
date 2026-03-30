package memory

import (
	"context"
	"fmt"

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

// nodeFromDoc deserialises a Firestore document into a KnowledgeNode.
// It uses doc.DataTo for all scalar/slice fields and handles the Vector32
// embedding type separately, since the Firestore SDK cannot coerce
// firestore.Vector32 → []float32 via reflection.
func nodeFromDoc(doc *firestore.DocumentSnapshot) (*KnowledgeNode, error) {
	var n KnowledgeNode
	if err := doc.DataTo(&n); err != nil {
		return nil, fmt.Errorf("nodeFromDoc %s: %w", doc.Ref.ID, err)
	}
	n.UUID = doc.Ref.ID
	if v, ok := doc.Data()["embedding"].(firestore.Vector32); ok {
		n.Embedding = []float32(v)
	}
	return &n, nil
}

// nodeWithLinksFromDoc deserialises a Firestore document into a KnowledgeNodeWithLinks,
// pulling entity_links and journal_entry_ids from the raw map (those fields are not
// on KnowledgeNode itself).
func nodeWithLinksFromDoc(doc *firestore.DocumentSnapshot) (KnowledgeNodeWithLinks, error) {
	n, err := nodeFromDoc(doc)
	if err != nil {
		return KnowledgeNodeWithLinks{}, err
	}
	data := doc.Data()
	return KnowledgeNodeWithLinks{
		KnowledgeNode:   *n,
		EntityLinks:     getStringSliceField(data, "entity_links"),
		JournalEntryIDs: getStringSliceField(data, "journal_entry_ids"),
	}, nil
}

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
