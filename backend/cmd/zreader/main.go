// Command zreader is the HTTP backend for the zreader self-hosted reader.
//
// It listens on 0.0.0.0:$PORT (default 8080) and serves both the REST API
// under /api/* and the embedded SPA on every other path.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/leafvmaple/zreader/internal/config"
	"github.com/leafvmaple/zreader/internal/server"
	"github.com/leafvmaple/zreader/internal/store"
)

// Version is the application version. Overridden via -ldflags at release time.
var Version = "0.1.0-dev"

func main() {
	defaultPort := 8080
	if v := os.Getenv("ZREADER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			defaultPort = n
		}
	}
	port := flag.Int("port", defaultPort, "HTTP listen port (env: ZREADER_PORT)")
	flag.Parse()

	logger := log.New(os.Stdout, "[zreader] ", log.LstdFlags|log.Lmicroseconds)
	logger.Printf("starting zreader %s on port %d", Version, *port)

	paths, err := config.Load()
	if err != nil {
		logger.Fatalf("config.Load: %v", err)
	}
	logger.Printf("data=%s library_roots=%v", paths.Data, paths.LibraryRoots)

	st, err := store.Open(paths.Data)
	if err != nil {
		logger.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Seed library_folders from ZREADER_LIBRARY_PATH on every boot. Duplicate
	// entries are idempotent (ErrFolderExists is swallowed).
	for _, root := range paths.LibraryRoots {
		if _, err := st.AddFolder(context.Background(), root); err != nil {
			logger.Printf("seed folder %s: %v", root, err)
		}
	}

	srv := server.New(server.Config{
		Port:    *port,
		Version: Version,
		Logger:  logger,
		Store:   st,
	})

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		logger.Fatalf("http server crashed: %v", err)
	case sig := <-stop:
		logger.Printf("received %s, shutting down", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Printf("graceful shutdown failed: %v", err)
		os.Exit(1)
	}
	logger.Printf("bye")
}
