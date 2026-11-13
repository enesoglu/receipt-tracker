// Package httpapi owns HTTP transport and the server lifecycle.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// Server owns the listener and shuts it down when its context is canceled.
type Server struct {
	http   *http.Server
	logger *slog.Logger
}

// New wires operational routes separately from future authenticated API routes.
func New(address string, logger *slog.Logger, spa http.Handler) *Server {
	router := chi.NewRouter()
	health := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("{\"status\":\"ok\"}\n")); err != nil {
			logger.Error("write health response", "error", err)
		}
	}
	router.Get("/healthz", health)
	router.Get("/livez", health)
	for _, prefix := range []string{"/api", "/auth", "/oauth", "/mcp", "/.well-known"} {
		router.Handle(prefix, http.NotFoundHandler())
		router.Handle(prefix+"/*", http.NotFoundHandler())
	}
	router.Handle("/*", spa)
	return &Server{http: &http.Server{Addr: address, Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}, logger: logger}
}

// Handler exposes the same routing used by the listener for transport tests.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Run stops accepting requests and drains active requests on cancellation.
func (s *Server) Run(ctx context.Context) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return s.Serve(ctx, listener)
}

// Serve owns listener until cancellation or an HTTP server failure.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	result := make(chan error, 1)
	go func() { result <- s.http.Serve(listener) }()
	s.logger.Info("server started", "listen_addr", listener.Addr().String())
	select {
	case err := <-result:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve http: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := s.http.Shutdown(shutdownCtx); err != nil {
			closeErr := s.http.Close()
			return fmt.Errorf("shutdown http: %w", errors.Join(err, closeErr))
		}
		if err := <-result; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("stop http: %w", err)
		}
		s.logger.Info("server stopped")
		return nil
	}
}
