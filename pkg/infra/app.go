package infra

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"cloud.google.com/go/firestore"
	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/panjf2000/ants/v2"
)

type appKeyType struct{}

var appKey = &appKeyType{}

// GDocLogFunc is the callback invoked by the app's gdoc log pool to write a line to the Google Doc.
// The caller (e.g. jot or main) provides this so infra does not depend on doc-writing logic.
type GDocLogFunc func(ctx context.Context, msg string)

// GeminiFactory creates a Gemini client and resolves model names. The caller (e.g. jot) provides this
// so infra does not depend on gemini implementation details (e.g. list-models, SanitizePrompt).
type GeminiFactory func(ctx context.Context, cfg *config.Config) (*genai.Client, string, string, error)

// gdocLogPayload carries context and message for the gdoc log pool.
type gdocLogPayload struct {
	ctx context.Context
	msg string
}

// WithApp returns a context that carries the given App.
func WithApp(ctx context.Context, app *App) context.Context {
	return context.WithValue(ctx, appKey, app)
}

// GetApp returns the App from the context, or nil if not set.
func GetApp(ctx context.Context) *App {
	if a, ok := ctx.Value(appKey).(*App); ok {
		return a
	}
	return nil
}

// App holds runtime dependencies (Firestore, Gemini, Logger, worker pools).
type App struct {
	Logger *slog.Logger

	firestoreClient       *firestore.Client
	firestoreErr          error
	geminiClient          *genai.Client
	geminiErr             error
	effectiveGeminiModel  string
	effectiveDreamerModel string
	configuredGeminiModel string
	configuredDreamerModel string

	gdocLogPool            *ants.PoolWithFunc
	toolExecutionPool      *ants.Pool
	asyncFireAndForgetPool *ants.Pool
	summaryGenerationPool  *ants.Pool
	backgroundTasksWg     *sync.WaitGroup
}

// Firestore returns the Firestore client for the app, or the error from creation.
func (a *App) Firestore(ctx context.Context) (*firestore.Client, error) {
	if a.firestoreErr != nil {
		return nil, a.firestoreErr
	}
	return a.firestoreClient, nil
}

// Gemini returns the Gemini client for the app, or the error from creation.
func (a *App) Gemini(ctx context.Context) (*genai.Client, error) {
	if a.geminiErr != nil {
		return nil, a.geminiErr
	}
	return a.geminiClient, nil
}

// EffectiveModel returns the resolved model name for API calls.
func (a *App) EffectiveModel(configured string) string {
	if configured == a.configuredGeminiModel && a.effectiveGeminiModel != "" {
		return a.effectiveGeminiModel
	}
	if configured == a.configuredDreamerModel && a.effectiveDreamerModel != "" {
		return a.effectiveDreamerModel
	}
	return configured
}

// SubmitAsync submits a task to the async fire-and-forget pool with WaitGroup tracking.
func (a *App) SubmitAsync(task func()) {
	if a.asyncFireAndForgetPool == nil {
		return
	}
	a.backgroundTasksWg.Add(1)
	a.asyncFireAndForgetPool.Submit(func() {
		defer a.backgroundTasksWg.Done()
		task()
	})
}

// SubmitToolExec submits a task to the tool execution pool with WaitGroup tracking.
func (a *App) SubmitToolExec(task func()) {
	if a.toolExecutionPool == nil {
		return
	}
	a.backgroundTasksWg.Add(1)
	a.toolExecutionPool.Submit(func() {
		defer a.backgroundTasksWg.Done()
		task()
	})
}

// SubmitToToolPool submits a task to the tool execution pool without WaitGroup tracking.
func (a *App) SubmitToToolPool(task func()) error {
	if a.toolExecutionPool == nil {
		return fmt.Errorf("no tool pool")
	}
	return a.toolExecutionPool.Submit(task)
}

// SubmitSummaryGen submits a task to the summary generation pool with WaitGroup tracking.
func (a *App) SubmitSummaryGen(task func()) {
	if a.summaryGenerationPool == nil {
		return
	}
	a.backgroundTasksWg.Add(1)
	a.summaryGenerationPool.Submit(func() {
		defer a.backgroundTasksWg.Done()
		task()
	})
}

// SubmitGDocLog submits a message to the Google Doc log pool with WaitGroup tracking.
func (a *App) SubmitGDocLog(ctx context.Context, msg string) {
	if a.gdocLogPool == nil {
		return
	}
	a.backgroundTasksWg.Add(1)
	a.gdocLogPool.Invoke(&gdocLogPayload{ctx: ctx, msg: msg})
}

// WaitForBackgroundTasks waits for all background tasks submitted to this app's pools to complete.
func (a *App) WaitForBackgroundTasks() {
	if a.backgroundTasksWg != nil {
		a.backgroundTasksWg.Wait()
	}
}

// WithContext returns a context that carries this App.
func (a *App) WithContext(ctx context.Context) context.Context {
	return WithApp(ctx, a)
}

var (
	defaultApp     *App
	defaultAppOnce sync.Once
	defaultAppErr  error
)

var errAppNotInitialized = errors.New("app not initialized: call InitDefaultApp at startup")

func getOrCreateApp(ctx context.Context) (*App, error) {
	if defaultApp != nil {
		return defaultApp, defaultAppErr
	}
	return nil, errAppNotInitialized
}

// GetDefaultApp returns the process-wide App.
func GetDefaultApp() (*App, error) {
	return getOrCreateApp(context.Background())
}

// InitDefaultApp initializes the process-wide App. Must be called at startup.
func InitDefaultApp(ctx context.Context, cfg *config.Config, gdocLog GDocLogFunc, geminiFactory GeminiFactory) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	defaultAppOnce.Do(func() {
		defaultApp, defaultAppErr = NewApp(ctx, cfg, gdocLog, geminiFactory)
	})
	return defaultAppErr
}

// NewApp creates a new App with Firestore, Gemini (via factory), and worker pools.
func NewApp(ctx context.Context, cfg *config.Config, gdocLog GDocLogFunc, geminiFactory GeminiFactory) (*App, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	logger := Logger
	if logger == nil {
		logger = slog.Default()
	}

	app := &App{
		Logger:                 logger,
		backgroundTasksWg:      &sync.WaitGroup{},
		configuredGeminiModel:  cfg.GeminiModel,
		configuredDreamerModel: cfg.DreamerModel,
	}

	app.firestoreClient, app.firestoreErr = firestore.NewClient(ctx, cfg.GoogleCloudProject)
	if app.firestoreErr != nil {
		return app, app.firestoreErr
	}

	if geminiFactory != nil {
		app.geminiClient, app.effectiveGeminiModel, app.effectiveDreamerModel, app.geminiErr = geminiFactory(ctx, cfg)
		if app.geminiErr != nil {
			return app, app.geminiErr
		}
	}

	if gdocLog != nil {
		gdocLogPool, err := ants.NewPoolWithFunc(1, func(i interface{}) {
			if p, ok := i.(*gdocLogPayload); ok {
				defer app.backgroundTasksWg.Done()
				gdocLog(p.ctx, p.msg)
			}
		}, ants.WithMaxBlockingTasks(10000))
		if err != nil {
			app.Logger.Error("failed to create gdoc log pool", "error", err)
		} else {
			app.gdocLogPool = gdocLogPool
		}
	}

	toolExecutionPool, err := ants.NewPool(8, ants.WithMaxBlockingTasks(100))
	if err != nil {
		app.Logger.Error("failed to create tool execution pool", "error", err)
	} else {
		app.toolExecutionPool = toolExecutionPool
	}

	asyncFireAndForgetPool, err := ants.NewPool(4, ants.WithMaxBlockingTasks(1000))
	if err != nil {
		app.Logger.Error("failed to create async fire-and-forget pool", "error", err)
	} else {
		app.asyncFireAndForgetPool = asyncFireAndForgetPool
	}

	summaryGenerationPool, err := ants.NewPool(3, ants.WithMaxBlockingTasks(100))
	if err != nil {
		app.Logger.Error("failed to create summary generation pool", "error", err)
	} else {
		app.summaryGenerationPool = summaryGenerationPool
	}

	return app, nil
}
