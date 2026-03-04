package jot

import (
	"context"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// PendingQuestion is a gap or contradiction detected during Dreamer synthesis, to be clarified by the user.
type PendingQuestion struct {
	UUID            string   `firestore:"-" json:"uuid"`
	Question        string   `firestore:"question" json:"question"`
	Kind            string   `firestore:"kind" json:"kind"` // "gap" or "contradiction"
	Context         string   `firestore:"context" json:"context,omitempty"`
	SourceEntryIDs  []string `firestore:"source_entry_ids" json:"source_entry_ids,omitempty"`
	CreatedAt       string   `firestore:"created_at" json:"created_at"`
	ResolvedAt      string   `firestore:"resolved_at" json:"resolved_at,omitempty"`
	Answer          string   `firestore:"answer" json:"answer,omitempty"`
}

// InsertPendingQuestions writes one or more pending questions to Firestore.
func InsertPendingQuestions(ctx context.Context, questions []PendingQuestion) error {
	if len(questions) == 0 {
		return nil
	}
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	for i := range questions {
		q := &questions[i]
		if q.UUID == "" {
			q.UUID = GenerateUUID()
		}
		if q.CreatedAt == "" {
			q.CreatedAt = now
		}
		_, err = client.Collection(PendingQuestionsCollection).Doc(q.UUID).Set(ctx, map[string]interface{}{
			"question":         q.Question,
			"kind":             q.Kind,
			"context":          q.Context,
			"source_entry_ids": q.SourceEntryIDs,
			"created_at":       q.CreatedAt,
			"resolved_at":      q.ResolvedAt,
			"answer":           q.Answer,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// GetUnresolvedPendingQuestions returns pending questions that have not been resolved, newest first.
func GetUnresolvedPendingQuestions(ctx context.Context, limit int) ([]PendingQuestion, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	// Fetch by created_at desc and filter unresolved in memory to avoid composite index
	iter := client.Collection(PendingQuestionsCollection).
		OrderBy("created_at", firestore.Desc).
		Limit(100).
		Documents(ctx)
	defer iter.Stop()
	var out []PendingQuestion
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, WrapFirestoreIndexError(err)
		}
		data := doc.Data()
		if getStringField(data, "resolved_at") != "" {
			continue
		}
		q := PendingQuestion{
			UUID:       doc.Ref.ID,
			Question:   getStringField(data, "question"),
			Kind:       getStringField(data, "kind"),
			Context:    getStringField(data, "context"),
			CreatedAt:  getStringField(data, "created_at"),
			ResolvedAt: getStringField(data, "resolved_at"),
			Answer:     getStringField(data, "answer"),
		}
		q.SourceEntryIDs = getStringSliceField(data, "source_entry_ids")
		out = append(out, q)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ResolvePendingQuestion sets resolved_at and answer for a pending question.
func ResolvePendingQuestion(ctx context.Context, uuid, answer string) error {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	_, err = client.Collection(PendingQuestionsCollection).Doc(uuid).Update(ctx, []firestore.Update{
		{Path: "resolved_at", Value: now},
		{Path: "answer", Value: answer},
	})
	return err
}
