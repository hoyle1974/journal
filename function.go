// Package jot provides the Jot API Cloud Function entry point and server wiring.
package jot

import (
	"context"
	"log"
	"net/http"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/gdoc"
	"github.com/jackstrohm/jot/internal/service"
	"github.com/jackstrohm/jot/pkg/infra"
)

var (
	defaultServer      *api.Server
	defaultConfig      *config.Config
	testConfigOverride *config.Config
)

func getConfig() *config.Config {
	if testConfigOverride != nil {
		return testConfigOverride
	}
	return defaultConfig
}

// InitDefaultApp loads config, initializes observability and the default infra.App (with gdoc logging).
// Call from cmd/server or tests before using the API. Returns an error if config load or app init fails.
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

func init() {
	functions.HTTP("JotAPI", JotAPI)
	if err := InitDefaultApp(context.Background()); err != nil {
		log.Printf("init default app failed: %v", err)
		return
	}
	app, _ := infra.GetDefaultApp()
	if app != nil {
		if err := service.RunFirstRunOnboarding(context.Background(), app); err != nil {
			log.Printf("first-run onboarding skipped: %v", err)
		}
	}
	journalSvc := service.NewJournalService()
	memorySvc := service.NewMemoryService()
	agentSvc := service.NewAgentService(app)
	smsSvc := service.NewSMSService(getConfig)
	systemSvc := service.NewSystemService(app)
	defaultServer = api.NewServer(app, defaultConfig, infra.Logger, journalSvc, memorySvc, agentSvc, smsSvc, systemSvc)
}

// JotAPI is the HTTP handler for the Cloud Function.
func JotAPI(w http.ResponseWriter, r *http.Request) {
	if defaultServer == nil {
		infra.Logger.Error("server not initialized")
		api.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	defaultServer.ServeHTTP(w, r)
}
