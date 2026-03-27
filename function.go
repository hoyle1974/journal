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
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/service"
)

var (
	defaultServer *api.Server
	defaultConfig *config.Config
	serverOnce    sync.Once
	serverInitErr error
)

// InitDefaultApp loads config, initializes observability and the default infra.App.
// Call from cmd/server or tests before using the API so startup fails fast on misconfiguration.
// When running as a Cloud Function with no prior call, the first request will perform this init lazily.
func InitDefaultApp(ctx context.Context) error {
	var err error
	defaultConfig, err = config.Load()
	if err != nil {
		return err
	}
	infra.InitObservability(defaultConfig)
	return infra.InitDefaultApp(ctx, defaultConfig, nil)
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
			serverInitErr = infra.InitDefaultApp(context.Background(), defaultConfig, nil)
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
		telegramSvc := service.NewTelegramService(func() *config.Config { return defaultConfig })
		systemSvc := service.NewSystemService(app)
		defaultServer = api.NewServer(app, defaultConfig, infra.Logger, journalSvc, memorySvc, agentSvc, telegramSvc, systemSvc)
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
