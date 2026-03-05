package api

import (
	"context"
	"net/http"

	"cloud.google.com/go/firestore"
	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/config"
	"log/slog"
)

// AppLike is the interface the HTTP layer needs from the app (Firestore, Gemini, pools, context attachment).
// Implemented by *jot.App so that this package does not import jot.
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
}

// RouterFunc is the function that routes requests to handlers. It receives the Server so handlers can use s.App and s.Config.
type RouterFunc func(*Server, http.ResponseWriter, *http.Request)

// Server holds app, config, logger, and the router for struct-based dependency injection.
type Server struct {
	App    AppLike
	Config *config.Config
	Logger *slog.Logger
	router RouterFunc
}

// NewServer builds a Server. The router is called after the request context has the app attached.
func NewServer(app AppLike, cfg *config.Config, logger *slog.Logger, router RouterFunc) *Server {
	return &Server{
		App:    app,
		Config: cfg,
		Logger: logger,
		router: router,
	}
}

// ServeHTTP implements http.Handler. It runs the prelude (trace, attach app, span, auth, rate limit) then calls the router.
// The router is responsible for path dispatch and calling handlers; it receives *Server so handlers use s.App and s.Config.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Prelude is implemented by the jot package and invoked via the router.
	// The router is passed (s, w, r) and does: attach app to ctx, start span, check auth, rate limit, then switch on path.
	// So we delegate the full request handling to the router; the router is defined in jot and does prelude + dispatch.
	s.router(s, w, r)
}

