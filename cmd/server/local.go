// runLocal runs the Jot API as a plain HTTP server for local development (bypasses Functions Framework).
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	jot "github.com/jackstrohm/jot"
)

func runLocal() {
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
