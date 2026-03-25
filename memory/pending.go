package memory

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PendingQuestionsCollection is the Firestore collection name for pending questions.
const PendingQuestionsCollection = KnowledgeCollection

// ActiveQuestionStateCollection tracks which pending question is currently being
// asked to each client. Documents are keyed by clientID string.
const ActiveQuestionStateCollection = "telegram_question_state"

// PendingQuestion is a gap or contradiction detected during knowledge synthesis, to be clarified by the user.
type PendingQuestion struct {
	UUID           string    `firestore:"-" json:"uuid"`
	Question       string    `firestore:"question" json:"question"`
	Kind           string    `firestore:"kind" json:"kind"` // "gap" or "contradiction"
	Context        string    `firestore:"context" json:"context,omitempty"`
	SourceEntryIDs []string  `firestore:"source_entry_uuids" json:"source_entry_uuids,omitempty"`
	CreatedAt      string    `firestore:"created_at" json:"created_at"`
	ResolvedAt     string    `firestore:"resolved_at" json:"resolved_at,omitempty"`
	Answer         string    `firestore:"answer" json:"answer,omitempty"`
	Embedding      []float32 `firestore:"embedding,omitempty" json:"-"`
}

// InsertPendingQuestions writes one or more pending questions to Firestore.
func (s *Store) InsertPendingQuestions(ctx context.Context, questions []PendingQuestion) error {
	if len(questions) == 0 {
		return nil
	}

	// Filter out duplicates before writing.
	filtered, err := s.filterDuplicatePendingQuestions(ctx, questions)
	if err != nil {
		return fmt.Errorf("filter duplicate questions: %w", err)
	}
	if len(filtered) == 0 {
		return nil
	}
	questions = filtered

	now := time.Now().Format(time.RFC3339)
	for i := range questions {
		q := &questions[i]
		if q.UUID == "" {
			q.UUID = generateUUID()
		}
		if q.CreatedAt == "" {
			q.CreatedAt = now
		}
	}

	for i := 0; i < len(questions); i += firestoreMaxBatchSize {
		end := min(i+firestoreMaxBatchSize, len(questions))
		batch := s.db.Batch()
		for _, q := range questions[i:end] {
			ref := s.db.Collection(PendingQuestionsCollection).Doc(q.UUID)
			batch.Set(ref, map[string]any{
				"question":            q.Question,
				"kind":                q.Kind,
				"context":             q.Context,
				"source_entry_uuids":    q.SourceEntryIDs,
				"created_at":          q.CreatedAt,
				"resolved_at":         q.ResolvedAt,
				"answer":              q.Answer,
				"embedding":           q.Embedding,
				"node_type":           NodeTypePendingQuestion,
				"significance_weight": 0.1,
			})
		}
		if _, err = batch.Commit(ctx); err != nil {
			return fmt.Errorf("insert pending questions batch: %w", err)
		}
	}
	return nil
}

// GetPendingQuestion fetches a single pending question by its UUID.
func (s *Store) GetPendingQuestion(ctx context.Context, uuid string) (*PendingQuestion, error) {
	doc, err := s.db.Collection(PendingQuestionsCollection).Doc(uuid).Get(ctx)
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
func (s *Store) GetUnresolvedPendingQuestions(ctx context.Context, limit int) ([]PendingQuestion, error) {
	if limit <= 0 {
		limit = 20
	}
	query := s.db.Collection(PendingQuestionsCollection).
		Where("node_type", "==", NodeTypePendingQuestion).
		OrderBy("created_at", firestore.Desc).
		Limit(100)
	out, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (PendingQuestion, error) {
		data := doc.Data()
		if getStringField(data, "resolved_at") != "" {
			return PendingQuestion{}, errSkipEntry
		}
		q := PendingQuestion{
			UUID:       doc.Ref.ID,
			Question:   getStringField(data, "question"),
			Kind:       getStringField(data, "kind"),
			Context:    getStringField(data, "context"),
			CreatedAt:  getStringField(data, "created_at"),
			ResolvedAt: getStringField(data, "resolved_at"),
			Answer:     getStringField(data, "answer"),
			Embedding:  getFloat32SliceField(data, "embedding"),
		}
		q.SourceEntryIDs = getStringSliceField(data, "source_entry_uuids")
		return q, nil
	})
	if err != nil {
		return nil, wrapFirestoreIndexError(err)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetRecentlyResolvedPendingQuestions returns pending questions resolved after `since`, newest first.
func (s *Store) GetRecentlyResolvedPendingQuestions(ctx context.Context, since time.Time) ([]PendingQuestion, error) {
	sinceStr := since.Format(time.RFC3339)
	query := s.db.Collection(PendingQuestionsCollection).
		Where("node_type", "==", NodeTypePendingQuestion).
		Where("created_at", ">=", sinceStr).
		OrderBy("created_at", firestore.Desc).
		Limit(200)
	out, err := queryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (PendingQuestion, error) {
		data := doc.Data()
		if getStringField(data, "resolved_at") == "" {
			return PendingQuestion{}, errSkipEntry
		}
		q := PendingQuestion{
			UUID:       doc.Ref.ID,
			Question:   getStringField(data, "question"),
			Kind:       getStringField(data, "kind"),
			Context:    getStringField(data, "context"),
			CreatedAt:  getStringField(data, "created_at"),
			ResolvedAt: getStringField(data, "resolved_at"),
			Answer:     getStringField(data, "answer"),
			Embedding:  getFloat32SliceField(data, "embedding"),
		}
		q.SourceEntryIDs = getStringSliceField(data, "source_entry_uuids")
		return q, nil
	})
	if err != nil {
		return nil, wrapFirestoreIndexError(err)
	}
	return out, nil
}

const dedupSimilarityThreshold = 0.85

// filterDuplicatePendingQuestions removes candidates semantically similar to existing questions.
func (s *Store) filterDuplicatePendingQuestions(ctx context.Context, candidates []PendingQuestion) ([]PendingQuestion, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}

	unresolved, err := s.GetUnresolvedPendingQuestions(ctx, 100)
	if err != nil {
		s.log.Warn("dedup: failed to fetch unresolved questions, skipping dedup", "error", err)
		return candidates, nil
	}
	since := time.Now().AddDate(0, 0, -30)
	resolved, err := s.GetRecentlyResolvedPendingQuestions(ctx, since)
	if err != nil {
		s.log.Warn("dedup: failed to fetch resolved questions, skipping dedup", "error", err)
		return candidates, nil
	}

	existing := make([]PendingQuestion, 0, len(unresolved)+len(resolved))
	existing = append(existing, unresolved...)
	existing = append(existing, resolved...)
	if len(existing) > 200 {
		existing = existing[:200]
	}
	if len(existing) == 0 {
		return candidates, nil
	}

	texts := make([]string, len(candidates))
	for i, c := range candidates {
		texts[i] = c.Question
	}
	vecs, err := s.embedder.GenerateEmbeddingsBatch(ctx, texts, EmbedTaskRetrievalDocument)
	if err != nil {
		s.log.Warn("dedup: embedding failed, inserting all candidates unfiltered", "error", err)
		return candidates, nil
	}
	for i := range candidates {
		candidates[i].Embedding = vecs[i]
	}

	kept := make([]PendingQuestion, 0, len(candidates))
	for _, c := range candidates {
		duplicate := false
		for _, ex := range existing {
			if len(ex.Embedding) == 0 {
				continue
			}
			sim := cosineSimilarity(c.Embedding, ex.Embedding)
			if sim >= dedupSimilarityThreshold {
				s.log.Info("dedup: dropping similar question",
					"candidate", c.Question,
					"matched", ex.Question,
					"similarity", sim,
				)
				duplicate = true
				break
			}
		}
		if !duplicate {
			kept = append(kept, c)
		}
	}
	return kept, nil
}

// ResolvePendingQuestion sets resolved_at and answer for a pending question.
func (s *Store) ResolvePendingQuestion(ctx context.Context, uuid, answer string) error {
	ref := s.db.Collection(PendingQuestionsCollection).Doc(uuid)
	doc, getErr := ref.Get(ctx)
	var kind string
	if getErr == nil {
		kind = getStringField(doc.Data(), "kind")
	}
	now := time.Now().Format(time.RFC3339)
	_, err := ref.Update(ctx, []firestore.Update{
		{Path: "resolved_at", Value: now},
		{Path: "answer", Value: answer},
	})
	if err != nil {
		return err
	}
	if kind == "onboarding" && answer != "" {
		bgCtx := context.WithoutCancel(ctx)
		sCopy := s
		go func() {
			if _, upErr := sCopy.UpsertSemanticMemory(bgCtx, answer, "user_identity", "selfmodel", 1.0, nil, nil); upErr != nil {
				sCopy.log.Warn("onboarding answer upsert failed", "error", upErr)
			}
			if upErr := sCopy.UpsertOwnerName(bgCtx, answer); upErr != nil {
				sCopy.log.Warn("onboarding owner name upsert failed", "error", upErr)
			}
		}()
	}
	return nil
}

// GetActiveQuestion returns the pending question currently being asked to the given client,
// or nil if none is active or the question was already resolved.
func (s *Store) GetActiveQuestion(ctx context.Context, clientID string) (*PendingQuestion, error) {
	doc, err := s.db.Collection(ActiveQuestionStateCollection).Doc(clientID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get active question state: %w", err)
	}
	questionUUID := getStringField(doc.Data(), "question_uuid")
	if questionUUID == "" {
		return nil, nil
	}
	q, err := s.GetPendingQuestion(ctx, questionUUID)
	if err != nil {
		return nil, fmt.Errorf("get active question: %w", err)
	}
	if q.ResolvedAt != "" {
		return nil, nil
	}
	return q, nil
}

// SetActiveQuestion records that questionUUID is the question currently being asked to clientID.
func (s *Store) SetActiveQuestion(ctx context.Context, clientID, questionUUID string) error {
	_, err := s.db.Collection(ActiveQuestionStateCollection).Doc(clientID).Set(ctx, map[string]any{
		"question_uuid": questionUUID,
		"set_at":        time.Now().Format(time.RFC3339),
	})
	return err
}

// ClearActiveQuestion removes the active question state for clientID.
func (s *Store) ClearActiveQuestion(ctx context.Context, clientID string) error {
	_, err := s.db.Collection(ActiveQuestionStateCollection).Doc(clientID).Delete(ctx)
	return err
}
