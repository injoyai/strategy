// Package server owns the HTTP listener lifecycle for researchd and the
// /api/v1 contract surface. M0-03 completes the shell: request IDs,
// authentication, idempotency keys, single-point error mapping and shared
// pagination live in the sibling files; this file only wraps http.Server.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/injoyai/strategy/internal/config"
)

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 1 << 20
)

// Server wraps http.Server with process lifecycle helpers.
type Server struct {
	srv *http.Server
}

// New builds the server around a root handler.
func New(cfg config.HTTP, handler http.Handler) *Server {
	return &Server{
		srv: &http.Server{
			Addr:              cfg.Addr,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
		},
	}
}

// ListenAndServe starts serving and blocks until Shutdown or an error.
func (s *Server) ListenAndServe() error {
	err := s.srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown stops accepting new connections and waits for in-flight requests
// until ctx expires.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
