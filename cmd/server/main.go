// Command server runs the Jot API as a standalone HTTP server.
// This is used for container-based deployment to Cloud Run.
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
