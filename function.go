// Package jot provides the Jot API Cloud Function entry point and server wiring.
// It avoids init()-time side effects: the HTTP handler is registered in init(), but
// config and server are created lazily on first request (or explicitly via InitDefaultApp
// and SetServer) so the package can be imported for tests or embedding without
// triggering config load or infra initialization.
package jot

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/gdoc"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/service"
)

var (
	defaultServer      *api.Server
	defaultConfig      *config.Config
	testConfigOverride *config.Config
	serverOnce         sync.Once
	serverInitErr      error
)

func getConfig() *config.Config {
	if testConfigOverride != nil {
		return testConfigOverride
	}
	return defaultConfig
}

// InitDefaultApp loads config, initializes observability and the default infra.App (with gdoc logging).
// Call from cmd/server or tests before using the API so startup fails fast on misconfiguration.
// When running as a Cloud Function with no prior call, the first request will perform this init lazily.
func InitDefaultApp(ctx context.Context) error {
	var err error
	defaultConfig, err = config.Load()
	if err != nil {
		return err
	}
	infra.InitObservability(defaultConfig)
	return infra.InitDefaultApp(ctx, defaultConfig, gdoc.NewGDocLogFunc(defaultConfig), nil)
}

// SetTestConfig sets a config override for tests. Returns a restore func to call in defer.
func SetTestConfig(cfg *config.Config) (restore func()) {
	old := testConfigOverride
	testConfigOverride = cfg
	return func() { testConfigOverride = old }
}

// SetServer injects the server for tests or embedding. If set before the first JotAPI call,
// that server is used and lazy initialization is skipped. Useful to avoid side effects when
// testing or embedding the handler.
func SetServer(s *api.Server) {
	serverOnce.Do(func() { defaultServer = s })
}

func init() {
	functions.HTTP("JotAPI", JotAPI)
}

func ensureServer() error {
	serverOnce.Do(func() {
		if defaultServer != nil {
			return
		}
		if defaultConfig == nil {
			var err error
			defaultConfig, err = config.Load()
			if err != nil {
				serverInitErr = err
				return
			}
			infra.InitObservability(defaultConfig)
			serverInitErr = infra.InitDefaultApp(context.Background(), defaultConfig, gdoc.NewGDocLogFunc(defaultConfig), nil)
			if serverInitErr != nil {
				return
			}
		}
		app, err := infra.GetDefaultApp()
		if err != nil || app == nil {
			serverInitErr = errors.New("app not initialized: call InitDefaultApp at startup or ensure config is loadable")
			return
		}
		if err := service.RunFirstRunOnboarding(context.Background(), app); err != nil {
			log.Printf("first-run onboarding skipped: %v", err)
		}
		journalSvc := service.NewJournalService(app, defaultConfig)
		memorySvc := service.NewMemoryService(app)
		agentSvc := service.NewAgentService(app)
		smsSvc := service.NewSMSService(getConfig)
		telegramSvc := service.NewTelegramService(getConfig)
		systemSvc := service.NewSystemService(app)
		defaultServer = api.NewServer(app, defaultConfig, infra.Logger, journalSvc, memorySvc, agentSvc, smsSvc, telegramSvc, systemSvc)
	})
	return serverInitErr
}

// JotAPI is the HTTP handler for the Cloud Function.
func JotAPI(w http.ResponseWriter, r *http.Request) {
	if defaultServer == nil {
		if err := ensureServer(); err != nil {
			infra.Logger.Error("server init failed", "error", err)
			api.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if defaultServer == nil {
			api.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
	}
	defaultServer.ServeHTTP(w, r)
}
