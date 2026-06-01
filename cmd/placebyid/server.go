package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// runServer creates the persistent browser engine, wires up handlers, and
// runs the HTTP server. ReadTimeout/WriteTimeout are 200s to accommodate the
// NestJS 180s deadline plus network overhead.
func runServer(port, concurrency int, langCode string, extractEmail, extraReviews bool, proxies string, inactivity time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eng := newHTTPEngine(ctx, concurrency, proxies, inactivity)
	defer eng.close()

	mux := http.NewServeMux()
	mux.Handle("GET /v1/places/{placeId}", placeHandler(eng, langCode, extractEmail, extraReviews))
	mux.Handle("POST /v1/places:searchText", searchTextHandler(eng, langCode))

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       200 * time.Second,
		WriteTimeout:      200 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("shutting down server...")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx) //nolint:errcheck
		cancel()
	}()

	log.Printf("placebyid server listening on %s  (Ctrl+C to stop)", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
