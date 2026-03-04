// Command local runs the Jot API as a plain HTTP server for local development.
// Bypasses the Functions Framework to avoid path routing issues.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	_ "github.com/jackstrohm/jot"
	jot "github.com/jackstrohm/jot"
	_ "github.com/jackstrohm/jot/internal/tools/impl"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		jot.JotAPI(w, r)
	})

	ctx := context.Background()
	if err := jot.InitDefaultApp(ctx); err != nil {
		log.Fatalf("init app: %v", err)
	}

	addr := ":" + port
	fmt.Printf("Jot API (local) listening on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
