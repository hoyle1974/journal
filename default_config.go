package jot

import (
	"log"
	"log/slog"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/pkg/infra"
)

// defaultConfig is set at startup from config.Load() so the rest of the package can read config without threading it everywhere.
var defaultConfig *config.Config

// Logger is the global structured logger; set after infra.InitObservability in init.
var Logger *slog.Logger

func init() {
	var err error
	defaultConfig, err = config.Load()
	if err != nil {
		log.Fatalf("config.Load: %v", err)
	}
	infra.InitObservability(defaultConfig)
	Logger = infra.Logger
}
