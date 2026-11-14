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

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/enesoglu/receipt-tracker/internal/auth"
	"github.com/enesoglu/receipt-tracker/internal/storage/drive"
)

// Server owns the listener and shuts it down when its context is canceled.
type Server struct {
	http   *http.Server
	logger *slog.Logger
}

// New builds the router: health checks, the REST API under /api/v1, the
// prefixes reserved for sign-in, OAuth and MCP, and the single-page app for
// everything else.
func New(address string, logger *slog.Logger, spa http.Handler, users *auth.Service, sessions *scs.SessionManager, storage *drive.Service) *Server {
	problems := problemWriter{logger: logger}
	authentication := authHandlers{users: users, sessions: sessions}
	if sessions != nil {
		sessions.ErrorFunc = func(w http.ResponseWriter, r *http.Request, _ error) {
			problems.write(w, r, errors.New("session storage failed"))
		}
	}
	router := chi.NewRouter()
	// Outermost first: the id exists before anything is logged, and the log
	// line sees the status that panic recovery wrote.
	router.Use(requestID, requestLog(logger), recoverer(problems))
	health := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("{\"status\":\"ok\"}\n")); err != nil {
			logger.Error("write health response", "error", err)
		}
	}
	router.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if storage == nil {
			health(w, r)
			return
		}
		state, err := storage.Health(r.Context())
		database, status := "ok", http.StatusOK
		if err != nil {
			database, status = "unavailable", http.StatusServiceUnavailable
		}
		if state != "ok" && state != "not_required" {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		overall := "ok"
		if status != http.StatusOK {
			overall = "unavailable"
		}
		if _, err := fmt.Fprintf(w, "{\"status\":%q,\"database\":%q,\"drive\":%q}\n", overall, database, state); err != nil {
			logger.Error("write health response", "error", err)
		}
	})
	router.Get("/livez", health)
	// The API answers in problem documents, also for paths it does not have.
	router.Route("/api", func(api chi.Router) {
		api.Use(crossOrigin(problems), bodyLimit(problems))
		api.NotFound(problems.notFound)
		api.MethodNotAllowed(problems.methodNotAllowed)
		api.Route("/v1", func(v1 chi.Router) {
			if sessions != nil {
				v1.Use(sessions.LoadAndSave)
			}
			v1.Use(RequireUser(users, sessions, problems.write), validateDriveQuery(problems))
			mountAPI(v1, problems, authentication, storageHandlers{storage: storage})
		})
	})
	authentication.mount(router, problems)
	// Reserved paths are never served by the single-page app.
	for _, prefix := range []string{"/auth", "/oauth", "/mcp", "/.well-known"} {
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
