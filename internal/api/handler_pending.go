package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/persona"
)

func handlePendingQuestions(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	questions, err := s.Memory.GetUnresolvedPendingQuestions(ctx, 20)
	if err != nil {
		infra.LoggerFrom(ctx).Error("pending questions list failed", "error", err)
		return nil, err
	}
	if app, ok := s.App.(*infra.App); ok {
		for i := range questions {
			if questions[i].Question != "" {
				questions[i].Question = persona.Apply(ctx, app, questions[i].Question, questions[i].Context)
			}
		}
	}
	return map[string]interface{}{"questions": questions, "count": len(questions)}, nil
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
	if err := s.Memory.ResolvePendingQuestion(ctx, questionID, strings.TrimSpace(body.Answer)); err != nil {
		infra.LoggerFrom(ctx).Error("resolve pending question failed", "id", questionID, "error", err)
		return nil, err
	}
	return map[string]string{"status": "ok", "id": questionID}, nil
}
