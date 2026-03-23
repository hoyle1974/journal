package infra

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"cloud.google.com/go/firestore"
	gcs "cloud.google.com/go/storage"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/hoyle1974/memory"
	memorygem "github.com/hoyle1974/memory/gemini"
	"github.com/jackstrohm/jot/pkg/storage"
	"google.golang.org/genai"
	"github.com/panjf2000/ants/v2"
)

// GeminiFactory creates a Gemini client and resolves the primary model name. The caller (e.g. jot) provides this
// so infra does not depend on gemini implementation details (e.g. list-models, SanitizePrompt).
type GeminiFactory func(ctx context.Context, cfg *config.Config) (*genai.Client, string, error)

// ToolEnv is the minimal interface tools need: config, Firestore, and single-shot LLM dispatch.
// Implemented by *App. Passed explicitly to tool execution so tools do not pull app from context.
type ToolEnv interface {
	Config() *config.Config
	Firestore(ctx context.Context) (*firestore.Client, error)
	Dispatch(ctx context.Context, req *LLMRequest) (*genai.GenerateContentResponse, error)
	MemoryStore() *memory.Store

	// Domain-specific accessors — prefer these over MemoryStore() for new code.
	MemoryEntries()   memory.EntryStore
	MemoryKnowledge() memory.KnowledgeStore
	MemoryGraph()     memory.GraphStore
	MemoryTasks()     memory.TaskStore
	MemoryContexts()  memory.ContextStore
	MemoryAgent()     memory.AgentOps
	MemoryAdmin()     memory.AdminOps
}

// App holds runtime dependencies (Firestore, Gemini, Logger, worker pools).
type App struct {
	Logger *slog.Logger
	Memory *memory.Store
	cfg    *config.Config

	firestoreClient        *firestore.Client
	firestoreErr           error
	geminiClient           *genai.Client
	geminiErr              error
	effectiveGeminiModel  string
	configuredGeminiModel string

	imageStorage storage.ImageStorage

	toolExecutionPool       *ants.Pool
	asyncFireAndForgetPool  *ants.Pool
	summaryGenerationPool   *ants.Pool
	backgroundTasksWg      *sync.WaitGroup
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
	return configured
}

// QueryModel returns the resolved model name for the main query agent.
func (a *App) QueryModel() string {
	if a.effectiveGeminiModel != "" {
		return a.effectiveGeminiModel
	}
	return a.configuredGeminiModel
}

// Config returns the config used to create the app (for callers that need project, API keys, etc.).
func (a *App) Config() *config.Config {
	return a.cfg
}

// Memory returns the memory store for the app.
func (a *App) MemoryStore() *memory.Store {
	return a.Memory
}

// MemoryEntries returns the EntryStore domain view of the memory store.
func (a *App) MemoryEntries()   memory.EntryStore     { return a.Memory.Entries() }

// MemoryKnowledge returns the KnowledgeStore domain view of the memory store.
func (a *App) MemoryKnowledge() memory.KnowledgeStore { return a.Memory.Knowledge() }

// MemoryGraph returns the GraphStore domain view of the memory store.
func (a *App) MemoryGraph()     memory.GraphStore     { return a.Memory.Graph() }

// MemoryTasks returns the TaskStore domain view of the memory store.
func (a *App) MemoryTasks()     memory.TaskStore      { return a.Memory.Tasks() }

// MemoryContexts returns the ContextStore domain view of the memory store.
func (a *App) MemoryContexts()  memory.ContextStore   { return a.Memory.Contexts() }

// MemoryAgent returns the AgentOps domain view of the memory store.
func (a *App) MemoryAgent()     memory.AgentOps       { return a.Memory.Agent() }

// MemoryAdmin returns the AdminOps domain view of the memory store.
func (a *App) MemoryAdmin()     memory.AdminOps       { return a.Memory.Admin() }

// App returns the app for use when the full *App is required (e.g. AddEntryAndEnqueue, EnqueueSaveQuery, ProcessEntry).
// Satisfies agent.FOHEnv and similar interfaces that need to pass *App explicitly.
func (a *App) App() *App {
	return a
}

// ImageStorage returns the image storage for uploading journal attachments. May be a no-op if JOT_IMAGES_BUCKET is not set.
func (a *App) ImageStorage() storage.ImageStorage {
	if a.imageStorage != nil {
		return a.imageStorage
	}
	return storage.NoopImageStorage()
}

// UploadAudio uploads raw audio bytes (audio/ogg) to GCS and returns the gs:// URI.
// Returns an error if JOT_IMAGES_BUCKET is not configured or the upload fails.
func (a *App) UploadAudio(ctx context.Context, data []byte) (string, error) {
	gcs, ok := a.imageStorage.(*storage.GCSImageStorage)
	if !ok || gcs == nil {
		return "", fmt.Errorf("audio upload not configured: set JOT_IMAGES_BUCKET to enable")
	}
	return gcs.UploadAudio(ctx, data)
}

// EnqueueTask creates a Cloud Task that POSTs the payload to the API at endpoint.
func (a *App) EnqueueTask(ctx context.Context, endpoint string, payload map[string]interface{}) error {
	return EnqueueTask(ctx, a.cfg, endpoint, payload)
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

// WaitForBackgroundTasks waits for all background tasks submitted to this app's pools to complete.
func (a *App) WaitForBackgroundTasks() {
	if a.backgroundTasksWg != nil {
		a.backgroundTasksWg.Wait()
	}
}

// WithContext attaches request-scoped logger to the context (no app in context).
func (a *App) WithContext(ctx context.Context) context.Context {
	return WithLogger(ctx, a.Logger)
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
func InitDefaultApp(ctx context.Context, cfg *config.Config, geminiFactory GeminiFactory) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	defaultAppOnce.Do(func() {
		defaultApp, defaultAppErr = NewApp(ctx, cfg, geminiFactory)
	})
	return defaultAppErr
}

// NewApp creates a new App with Firestore, Gemini (via factory), and worker pools.
func NewApp(ctx context.Context, cfg *config.Config, geminiFactory GeminiFactory) (*App, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	logger := Logger
	if logger == nil {
		logger = slog.Default()
	}

	app := &App{
		Logger:                 logger,
		cfg:                    cfg,
		backgroundTasksWg:      &sync.WaitGroup{},
		configuredGeminiModel: cfg.GeminiModel,
	}

	app.firestoreClient, app.firestoreErr = firestore.NewClient(ctx, cfg.GoogleCloudProject)
	if app.firestoreErr != nil {
		return app, app.firestoreErr
	}

	factory := geminiFactory
	if factory == nil {
		factory = DefaultGeminiFactory
	}
	app.geminiClient, app.effectiveGeminiModel, app.geminiErr = factory(ctx, cfg)
	if app.geminiErr != nil {
		return app, app.geminiErr
	}

	app.Memory = memory.New(
		app.firestoreClient,
		memorygem.NewEmbedder(cfg.GoogleCloudProject),
		memorygem.NewDispatcher(app.geminiClient, app.effectiveGeminiModel),
	)

	if cfg.ImagesBucket != "" {
		gcsClient, err := gcs.NewClient(ctx)
		if err != nil {
			app.Logger.Warn("GCS client for image upload failed, image attach disabled", "bucket", cfg.ImagesBucket, "error", err)
		} else {
			app.imageStorage = storage.NewGCSImageStorage(gcsClient, cfg.ImagesBucket)
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
