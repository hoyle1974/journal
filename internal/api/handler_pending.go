package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackstrohm/jot/internal/infra"
)

func handlePendingQuestions(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	questions, err := s.Memory.GetUnresolvedPendingQuestions(ctx, 20)
	if err != nil {
		infra.LoggerFrom(ctx).Error("pending questions list failed", "error", err)
		return nil, err
	}
	return map[string]any{"questions": questions, "count": len(questions)}, nil
}

func handlePendingQuestionResolve(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	questionID := chi.URLParam(r, "id")
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path, "question_id", questionID)
	var body struct {
		Answer string `json:"answer" validate:"required"`
	}
	if err := DecodeAndValidate(r, &body, s.Validator); err != nil {
		return nil, handlerError(http.StatusBadRequest, err.Error())
	}
	answer := strings.TrimSpace(body.Answer)

	// Fetch question before resolving so we have kind and text for ingest.
	q, fetchErr := s.Memory.GetPendingQuestion(ctx, questionID)

	if err := s.Memory.ResolvePendingQuestion(ctx, questionID, answer); err != nil {
		infra.LoggerFrom(ctx).Error("resolve pending question failed", "id", questionID, "error", err)
		return nil, err
	}

	// Ingest answered gap questions into the knowledge graph.
	if fetchErr == nil && q != nil && q.Kind == "gap" && answer != "" {
		bgCtx := context.WithoutCancel(ctx)
		qCopy := *q
		go func() {
			s.Agent.IngestGapAnswer(bgCtx, qCopy.Question, answer)
		}()
	}

	return map[string]string{"status": "ok", "id": questionID}, nil
}
