// Package server wires the HTTP router for the zreader backend.
//
// /api/v1/* serves the JSON REST API. Every other path is served by the
// embedded SPA (internal/webui), so a single port hosts both.
package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/leafvmaple/zreader/internal/store"
)

// Config holds runtime configuration for the server.
type Config struct {
	Port    int
	Version string
	Logger  *log.Logger
	Store   *store.Store
}

// Server is the typed HTTP server. The handlers/* files hang methods off it
// so they can reach Store/Logger without a global.
type Server struct {
	http  *http.Server
	cfg   Config
	store *store.Store
}

// New builds a Server but does not start it. Store is required.
func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Store == nil {
		panic("server.New: Store is required")
	}
	s := &Server{cfg: cfg, store: cfg.Store}
	s.http = &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", cfg.Port),
		Handler:           s.newRouter(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout off for /content (large slices); keep idle reasonable.
		IdleTimeout: 120 * time.Second,
	}
	return s
}

// ListenAndServe blocks until the server stops.
func (s *Server) ListenAndServe() error {
	s.cfg.Logger.Printf("listening on %s", s.http.Addr)
	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown attempts a graceful shutdown bounded by ctx.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
