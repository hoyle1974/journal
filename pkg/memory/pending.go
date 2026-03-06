package memory

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/pkg/infra"
)

// PendingQuestionsCollection is the Firestore collection name for pending questions.
const PendingQuestionsCollection = "pending_questions"

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
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	for i := range questions {
		q := &questions[i]
		if q.UUID == "" {
			q.UUID = infra.GenerateUUID()
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

// GetPendingQuestion fetches a single pending question by its UUID.
func GetPendingQuestion(ctx context.Context, uuid string) (*PendingQuestion, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := client.Collection(PendingQuestionsCollection).Doc(uuid).Get(ctx)
	if err != nil {
		return nil, err
	}
	var q PendingQuestion
	if err := doc.DataTo(&q); err != nil {
		return nil, err
	}
	q.UUID = doc.Ref.ID
	return &q, nil
}

// GetUnresolvedPendingQuestions returns pending questions that have not been resolved, newest first.
func GetUnresolvedPendingQuestions(ctx context.Context, limit int) ([]PendingQuestion, error) {
	client, err := infra.GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	query := client.Collection(PendingQuestionsCollection).
		OrderBy("created_at", firestore.Desc).
		Limit(100)
	out, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (PendingQuestion, error) {
		data := doc.Data()
		if infra.GetStringField(data, "resolved_at") != "" {
			return PendingQuestion{}, fmt.Errorf("skip")
		}
		q := PendingQuestion{
			UUID:       doc.Ref.ID,
			Question:   infra.GetStringField(data, "question"),
			Kind:       infra.GetStringField(data, "kind"),
			Context:    infra.GetStringField(data, "context"),
			CreatedAt:  infra.GetStringField(data, "created_at"),
			ResolvedAt: infra.GetStringField(data, "resolved_at"),
			Answer:     infra.GetStringField(data, "answer"),
		}
		q.SourceEntryIDs = infra.GetStringSliceField(data, "source_entry_ids")
		return q, nil
	})
	if err != nil {
		return nil, infra.WrapFirestoreIndexError(err)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ResolvePendingQuestion sets resolved_at and answer for a pending question.
func ResolvePendingQuestion(ctx context.Context, uuid, answer string) error {
	client, err := infra.GetFirestoreClient(ctx)
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
