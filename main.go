package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	listenAddress = "127.0.0.1:18317"
	upstreamBase  = "https://opencode.ai/zen/go/v1"
)

func main() {
	upstream, err := url.Parse(upstreamBase)
	if err != nil {
		log.Fatalf("invalid fixed upstream URL: %v", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 90 * time.Second
	client := &http.Client{Transport: transport}

	logger := log.New(os.Stderr, "muse-codex-adapter: ", log.LstdFlags)
	server := &http.Server{
		Addr:              listenAddress,
		Handler:           NewHandler(upstream, client),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          logger,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Printf("graceful shutdown failed: %v", err)
		}
	}()

	logger.Printf("listening on %s", listenAddress)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("server stopped: %v", err)
	}
}
