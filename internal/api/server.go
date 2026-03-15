package api

import (
	"context"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/config"
	"log/slog"
	"google.golang.org/genai"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
)

type serverContextKey struct{}

// ServerFromContext returns the *Server from the request context, or nil.
func ServerFromContext(ctx context.Context) *Server {
	if s, _ := ctx.Value(serverContextKey{}).(*Server); s != nil {
		return s
	}
	return nil
}

// AppLike is the interface the HTTP layer needs from the app (Firestore, Gemini, pools, context attachment, task enqueue).
// Implemented by *infra.App (via jot) so that handlers can enqueue Cloud Tasks (e.g. process-sms-query).
type AppLike interface {
	Firestore(context.Context) (*firestore.Client, error)
	Gemini(context.Context) (*genai.Client, error)
	EffectiveModel(configured string) string
	SubmitAsync(task func())
	SubmitToolExec(task func())
	SubmitSummaryGen(task func())
	SubmitGDocLog(ctx context.Context, msg string)
	WaitForBackgroundTasks()
	WithContext(ctx context.Context) context.Context
	// EnqueueTask enqueues a Cloud Task that POSTs payload to endpoint. Returns nil if JOT_API_URL or Cloud Tasks are unavailable.
	EnqueueTask(ctx context.Context, endpoint string, payload map[string]interface{}) error
}

// Server holds app, config, logger, domain services, validator, and the chi router for struct-based dependency injection.
type Server struct {
	App       AppLike
	Config    *config.Config
	Logger    *slog.Logger
	Journal   JournalService
	Memory    MemoryService
	Agent     AgentService
	SMS       SMSService
	Telegram  TelegramService
	System    SystemService
	Validator *validator.Validate
	Mux       *chi.Mux
}

// NewServer builds a Server with validator and chi router. Domain services (Journal, Memory, Agent, SMS, Telegram, System)
// must be non-nil for handlers that use them.
func NewServer(app AppLike, cfg *config.Config, logger *slog.Logger, journal JournalService, memory MemoryService, agent AgentService, sms SMSService, telegram TelegramService, system SystemService) *Server {
	validate := validator.New()
	s := &Server{
		App:      app,
		Config:   cfg,
		Logger:   logger,
		Journal:  journal,
		Memory:   memory,
		Agent:    agent,
		SMS:      sms,
		Telegram: telegram,
		System:   system,
		Validator: validate,
	}
	s.Mux = NewRouter(s)
	return s
}

// ServeHTTP implements http.Handler by delegating to the chi Mux.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Mux.ServeHTTP(w, r)
}

