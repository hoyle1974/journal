package memory

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PendingQuestionsCollection is the Firestore collection name for pending questions.
const PendingQuestionsCollection = "pending_questions"

// TelegramQuestionStateCollection tracks which pending question is currently being
// asked to each Telegram chat. Documents are keyed by string(chat_id).
const TelegramQuestionStateCollection = "telegram_question_state"

// PendingQuestion is a gap or contradiction detected during Dreamer synthesis, to be clarified by the user.
type PendingQuestion struct {
	UUID            string    `firestore:"-" json:"uuid"`
	Question        string    `firestore:"question" json:"question"`
	Kind            string    `firestore:"kind" json:"kind"` // "gap" or "contradiction"
	Context         string    `firestore:"context" json:"context,omitempty"`
	SourceEntryIDs  []string  `firestore:"source_entry_ids" json:"source_entry_ids,omitempty"`
	CreatedAt       string    `firestore:"created_at" json:"created_at"`
	ResolvedAt      string    `firestore:"resolved_at" json:"resolved_at,omitempty"`
	Answer          string    `firestore:"answer" json:"answer,omitempty"`
	Embedding       []float32 `firestore:"embedding,omitempty" json:"-"`
}

// InsertPendingQuestions writes one or more pending questions to Firestore.
func InsertPendingQuestions(ctx context.Context, env infra.ToolEnv, questions []PendingQuestion) error {
	if len(questions) == 0 {
		return nil
	}
	if env == nil {
		return fmt.Errorf("env required")
	}

	// Filter out duplicates before writing.
	filtered, err := filterDuplicatePendingQuestions(ctx, env, questions)
	if err != nil {
		return fmt.Errorf("filter duplicate questions: %w", err)
	}
	if len(filtered) == 0 {
		return nil
	}
	questions = filtered

	client, err := env.Firestore(ctx)
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
			"embedding":        q.Embedding,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// GetPendingQuestion fetches a single pending question by its UUID.
func GetPendingQuestion(ctx context.Context, env infra.ToolEnv, uuid string) (*PendingQuestion, error) {
	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
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
func GetUnresolvedPendingQuestions(ctx context.Context, env infra.ToolEnv, limit int) ([]PendingQuestion, error) {
	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
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
			Embedding:  infra.GetFloat32SliceField(data, "embedding"),
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

// GetRecentlyResolvedPendingQuestions returns pending questions resolved after `since`, newest first.
// Used by the dedup filter to avoid re-asking recently answered questions.
// Scans at most 200 documents server-side (created_at DESC); client-side filters resolved_at != "".
func GetRecentlyResolvedPendingQuestions(ctx context.Context, env infra.ToolEnv, since time.Time) ([]PendingQuestion, error) {
	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	sinceStr := since.Format(time.RFC3339)
	query := client.Collection(PendingQuestionsCollection).
		Where("created_at", ">=", sinceStr).
		OrderBy("created_at", firestore.Desc).
		Limit(200)
	out, err := infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (PendingQuestion, error) {
		data := doc.Data()
		if infra.GetStringField(data, "resolved_at") == "" {
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
			Embedding:  infra.GetFloat32SliceField(data, "embedding"),
		}
		q.SourceEntryIDs = infra.GetStringSliceField(data, "source_entry_ids")
		return q, nil
	})
	if err != nil {
		return nil, infra.WrapFirestoreIndexError(err)
	}
	return out, nil
}

const dedupSimilarityThreshold = 0.85

// filterDuplicatePendingQuestions removes candidates that are semantically similar
// to existing pending questions (unresolved or resolved within 30 days).
// If the embedding API fails, all candidates are returned unfiltered (best-effort).
func filterDuplicatePendingQuestions(ctx context.Context, env infra.ToolEnv, candidates []PendingQuestion) ([]PendingQuestion, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}

	// Fetch the comparison set.
	unresolved, err := GetUnresolvedPendingQuestions(ctx, env, 100)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("dedup: failed to fetch unresolved questions, skipping dedup", "error", err)
		return candidates, nil
	}
	since := time.Now().AddDate(0, 0, -30)
	resolved, err := GetRecentlyResolvedPendingQuestions(ctx, env, since)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("dedup: failed to fetch resolved questions, skipping dedup", "error", err)
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

	// Embed all candidates in one batch.
	projectID := env.Config().GoogleCloudProject
	texts := make([]string, len(candidates))
	for i, c := range candidates {
		texts[i] = c.Question
	}
	vecs, err := infra.GenerateEmbeddingsBatch(ctx, projectID, texts, infra.EmbedTaskRetrievalDocument)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("dedup: embedding failed, inserting all candidates unfiltered", "error", err)
		return candidates, nil
	}
	for i := range candidates {
		candidates[i].Embedding = vecs[i]
	}

	// Compare each candidate against every existing question.
	kept := make([]PendingQuestion, 0, len(candidates))
	for _, c := range candidates {
		duplicate := false
		for _, ex := range existing {
			if len(ex.Embedding) == 0 {
				continue // no stored embedding; can't compare
			}
			sim := utils.CosineSimilarity(c.Embedding, ex.Embedding)
			if sim >= dedupSimilarityThreshold {
				infra.LoggerFrom(ctx).Info("dedup: dropping similar question",
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
// If the question has kind "onboarding", the answer is also upserted as a user_identity
// knowledge node in a goroutine (non-blocking). env supplies Firestore and Config; pass from the caller.
func ResolvePendingQuestion(ctx context.Context, env infra.ToolEnv, uuid, answer string) error {
	if env == nil {
		return fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return err
	}
	ref := client.Collection(PendingQuestionsCollection).Doc(uuid)
	doc, getErr := ref.Get(ctx)
	var kind string
	if getErr == nil {
		kind = infra.GetStringField(doc.Data(), "kind")
	}
	now := time.Now().Format(time.RFC3339)
	_, err = ref.Update(ctx, []firestore.Update{
		{Path: "resolved_at", Value: now},
		{Path: "answer", Value: answer},
	})
	if err != nil {
		return err
	}
	if kind == "onboarding" && answer != "" {
		bgCtx := context.WithoutCancel(ctx)
		envCopy := env
		go func() {
			if _, upErr := UpsertSemanticMemory(bgCtx, envCopy, answer, "user_identity", "selfmodel", 1.0, nil, nil); upErr != nil {
				infra.LoggerFrom(bgCtx).Warn("onboarding answer upsert failed", "error", upErr)
			}
		}()
	}
	return nil
}

// GetTelegramActiveQuestion returns the pending question currently being asked to
// the given Telegram chat, or nil if none is active or the question was already resolved.
func GetTelegramActiveQuestion(ctx context.Context, env infra.ToolEnv, chatID int64) (*PendingQuestion, error) {
	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := client.Collection(TelegramQuestionStateCollection).Doc(strconv.FormatInt(chatID, 10)).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get telegram question state: %w", err)
	}
	questionUUID := infra.GetStringField(doc.Data(), "question_uuid")
	if questionUUID == "" {
		return nil, nil
	}
	q, err := GetPendingQuestion(ctx, env, questionUUID)
	if err != nil {
		return nil, fmt.Errorf("get active question: %w", err)
	}
	// If it was already resolved by another client, treat as no active question.
	if q.ResolvedAt != "" {
		return nil, nil
	}
	return q, nil
}

// SetTelegramActiveQuestion records that questionUUID is the question currently
// being asked to chatID. Overwrites any previous state.
func SetTelegramActiveQuestion(ctx context.Context, env infra.ToolEnv, chatID int64, questionUUID string) error {
	if env == nil {
		return fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(TelegramQuestionStateCollection).Doc(strconv.FormatInt(chatID, 10)).Set(ctx, map[string]any{
		"question_uuid": questionUUID,
		"set_at":        time.Now().Format(time.RFC3339),
	})
	return err
}

// ClearTelegramActiveQuestion removes the active question state for chatID.
func ClearTelegramActiveQuestion(ctx context.Context, env infra.ToolEnv, chatID int64) error {
	if env == nil {
		return fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return err
	}
	_, err = client.Collection(TelegramQuestionStateCollection).Doc(strconv.FormatInt(chatID, 10)).Delete(ctx)
	return err
}
