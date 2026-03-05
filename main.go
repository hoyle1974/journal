package jot

import (
	"context"
	"log"
	"net/http"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/jackstrohm/jot/internal/api"
	"github.com/jackstrohm/jot/internal/config"
)

// defaultServer is the HTTP handler for the Cloud Function; set in init after InitDefaultApp.
var defaultServer *api.Server

// testConfigOverride is set by SetTestConfig in tests so IsAllowedPhoneNumber and config use it instead of defaultConfig.
var testConfigOverride *config.Config

// SetTestConfig sets a config override for tests. Returns a restore func to call in defer.
func SetTestConfig(cfg *config.Config) (restore func()) {
	old := testConfigOverride
	testConfigOverride = cfg
	return func() { testConfigOverride = old }
}

func init() {
	functions.HTTP("JotAPI", JotAPI)
	api.StartRateLimitCleanup()
	if err := InitDefaultApp(context.Background()); err != nil {
		log.Printf("init default app failed: %v", err)
		return
	}
	defaultServer = api.NewServer(defaultApp, defaultConfig, Logger, JotBackend, api.Router)
}

func getConfig() *config.Config {
	if testConfigOverride != nil {
		return testConfigOverride
	}
	return defaultConfig
}

func JotAPI(w http.ResponseWriter, r *http.Request) {
	if defaultServer == nil {
		Logger.Error("server not initialized")
		api.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	defaultServer.ServeHTTP(w, r)
}
