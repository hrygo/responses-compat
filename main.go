package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
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
	if err := runCommand(os.Args[1:], os.Stderr, runServer); err != nil {
		fmt.Fprintf(os.Stderr, "responses-compat: %v\n", err)
		os.Exit(1)
	}
}

func runCommand(args []string, stderr io.Writer, start func(RuntimeConfig) error) error {
	if stderr == nil {
		stderr = io.Discard
	}
	flags := flag.NewFlagSet("responses-compat", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to the JSON configuration file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected positional argument %q", flags.Arg(0))
	}
	if start == nil {
		return errors.New("server startup callback is required")
	}
	config, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	return start(config)
}

func runServer(config RuntimeConfig) error {
	handler, err := NewConfiguredHandler(config, newUpstreamClient())
	if err != nil {
		return err
	}
	logger := log.New(os.Stderr, "responses-compat: ", log.LstdFlags)
	server := &http.Server{
		Addr:              config.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          logger,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Printf("graceful shutdown failed: %v", err)
		}
	}()

	logger.Printf("listening on %s", config.Listen)
	err = server.ListenAndServe()
	stop()
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func newUpstreamClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 90 * time.Second
	return &http.Client{Transport: transport}
}
