//go:build sqlite || !dynamodb

package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	store, err := NewSQLiteStore()
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	mux := initHandlers(store)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Public TRixie starting on http://localhost:%s", port)
	log.Printf("Version: %s, Commit: %s, Built: %s", Version, GitCommit, BuildTime)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
