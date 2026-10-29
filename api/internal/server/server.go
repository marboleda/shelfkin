// Package server is the Shelfkin HTTP API server: the endpoint
// implementations, the middleware around them and the http.Server lifecycle.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// Timeouts of the http.Server. WriteTimeout covers the whole handler, so it
// must stay well above HealthTimeout and any database work.
const (
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 15 * time.Second
	WriteTimeout      = 30 * time.Second
	IdleTimeout       = 120 * time.Second
)

// ShutdownTimeout bounds how long in-flight requests get to finish after a
// shutdown signal. Cloud Run sends SIGKILL 10s after SIGTERM, so this leaves
// a moment to close the database pool.
const ShutdownTimeout = 9 * time.Second

// NewHandler returns the full HTTP handler: the routes generated from the
// OpenAPI spec for ssi, wrapped in request logging, panic recovery and
// Error-format 404 and 405 responses. No routes are registered by hand.
func NewHandler(ssi openapi.StrictServerInterface, logger *slog.Logger) http.Handler {
	badRequest := func(w http.ResponseWriter, _ *http.Request, err error) {
		writeError(w, http.StatusBadRequest, CodeBadRequest, err.Error())
	}
	strict := openapi.NewStrictHandlerWithOptions(ssi, nil, openapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: badRequest,
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.ErrorContext(r.Context(), "handler error",
				"method", r.Method, "path", r.URL.Path, "error", err)
			writeInternal(w)
		},
	})
	mux := http.NewServeMux()
	openapi.HandlerWithOptions(strict, openapi.StdHTTPServerOptions{
		BaseRouter:       mux,
		ErrorHandlerFunc: badRequest,
	})

	var h http.Handler = jsonFallbacks(mux)
	h = recoverPanics(logger, h)
	h = logRequests(logger, h)
	return h
}

// NewHTTPServer returns an http.Server for handler with every timeout set.
func NewHTTPServer(handler http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}

// Serve serves srv on ln until ctx is cancelled, then shuts down gracefully:
// it stops accepting connections and waits up to shutdownTimeout for
// in-flight requests to finish. It returns nil after a clean shutdown.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		// In-flight requests did not finish in time; drop them.
		_ = srv.Close()
	}
	if serr := <-serveErr; serr != nil && !errors.Is(serr, http.ErrServerClosed) {
		return errors.Join(err, serr)
	}
	return err
}
