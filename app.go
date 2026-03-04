package jot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"cloud.google.com/go/firestore"
	"github.com/google/generative-ai-go/genai"
	"github.com/panjf2000/ants/v2"
)

type appKeyType struct{}

var appKey = &appKeyType{}

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
// It is created once by InitDefaultApp at startup and attached to the request context.
type App struct {
	Logger *slog.Logger

	// Firestore
	firestoreClient *firestore.Client
	firestoreErr    error

	// Gemini
	geminiClient         *genai.Client
	geminiErr            error
	effectiveGeminiModel string
	effectiveDreamerModel string

	// Worker pools and wait group for request-scoped background tasks
	gdocLogPool         *ants.PoolWithFunc
	toolExecutionPool   *ants.Pool
	asyncFireAndForgetPool *ants.Pool
	summaryGenerationPool *ants.Pool
	backgroundTasksWg   *sync.WaitGroup
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

// EffectiveModel returns the resolved model name for API calls (avoids v1beta 404s).
func (a *App) EffectiveModel(configured string) string {
	if configured == GeminiModel && a.effectiveGeminiModel != "" {
		return a.effectiveGeminiModel
	}
	if configured == DreamerModel && a.effectiveDreamerModel != "" {
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
// Used by the query agent to run tools in parallel within a request. Returns error if submission fails.
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

var (
	defaultApp     *App
	defaultAppOnce sync.Once
	defaultAppErr  error
)

// errAppNotInitialized is returned by getOrCreateApp when the app was never initialized via InitDefaultApp.
var errAppNotInitialized = errors.New("app not initialized: call InitDefaultApp at startup")

// getOrCreateApp returns the process-wide App. It does not create the app; call InitDefaultApp at startup.
func getOrCreateApp(ctx context.Context) (*App, error) {
	if defaultApp != nil {
		return defaultApp, defaultAppErr
	}
	return nil, errAppNotInitialized
}

// GetDefaultApp returns the process-wide App (e.g. for the gdoc log appender when ctx has no App).
// Returns errAppNotInitialized if InitDefaultApp was never called or failed.
func GetDefaultApp() (*App, error) {
	return getOrCreateApp(context.Background())
}

// InitDefaultApp initializes the process-wide App. Must be called at startup before serving (e.g. in main or init).
// Subsequent calls are no-ops; returns the error from the first run if initialization failed.
func InitDefaultApp(ctx context.Context) error {
	defaultAppOnce.Do(func() {
		defaultApp, defaultAppErr = NewApp(ctx)
	})
	return defaultAppErr
}

// NewApp creates a new App with Firestore client, Gemini client, and worker pools.
// Config (env/secrets) must already be loaded via config and observability init.
func NewApp(ctx context.Context) (*App, error) {
	logger := Logger
	if logger == nil {
		logger = slog.Default()
	}

	app := &App{
		Logger:             logger,
		backgroundTasksWg:  &sync.WaitGroup{},
	}

	// Firestore
	app.firestoreClient, app.firestoreErr = firestore.NewClient(ctx, GoogleCloudProject)
	if app.firestoreErr != nil {
		return app, app.firestoreErr
	}

	// Gemini (resolves model names)
	app.geminiClient, app.effectiveGeminiModel, app.effectiveDreamerModel, app.geminiErr = newGeminiClientForApp(ctx, logger)
	if app.geminiErr != nil {
		return app, app.geminiErr
	}

	// Pools (gdoc worker captures app for Done() and logToGDocSync gets App from ctx)
	gdocLogPool, err := ants.NewPoolWithFunc(1, func(i interface{}) {
		if p, ok := i.(*gdocLogPayload); ok {
			defer app.backgroundTasksWg.Done()
			logToGDocSync(p.ctx, p.msg)
		}
	}, ants.WithMaxBlockingTasks(10000))
	if err != nil {
		app.Logger.Error("failed to create gdoc log pool", "error", err)
	} else {
		app.gdocLogPool = gdocLogPool
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
