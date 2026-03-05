// Command server runs the Jot API: use RUN_LOCAL=1 for plain HTTP (local dev), else Functions Framework (Cloud Run).
package main

import (
	"context"
	"log"
	"os"

	"github.com/GoogleCloudPlatform/functions-framework-go/funcframework"
	jot "github.com/jackstrohm/jot"
	_ "github.com/jackstrohm/jot/internal/tools/impl"
)

func main() {
	if os.Getenv("RUN_LOCAL") == "1" || os.Getenv("RUN_LOCAL") == "true" {
		runLocal()
		return
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := jot.InitDefaultApp(context.Background()); err != nil {
		log.Fatalf("init app: %v", err)
	}
	if err := funcframework.Start(port); err != nil {
		log.Fatalf("funcframework.Start: %v", err)
	}
}
