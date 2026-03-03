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

	// Pre-warm default App at startup so first /dream doesn't wait for slow init
	go func() {
		ctx := context.Background()
		if err := jot.InitDefaultApp(ctx); err != nil {
			log.Printf("pre-warm: %v (will retry on first request)", err)
		} else {
			log.Print("app pre-warmed")
		}
	}()

	addr := ":" + port
	fmt.Printf("Jot API (local) listening on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
