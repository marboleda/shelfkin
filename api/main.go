// Command api is the Shelfkin HTTP API server.
//
// It reads DATABASE_URL (required), PORT (default 8080) and LOG_LEVEL
// (default info) from the environment, logs JSON to stdout and shuts down
// gracefully on SIGTERM or SIGINT. See the README for how to run it locally.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/internal/config"
	"github.com/marboleda/shelfkin/api/internal/logging"
	"github.com/marboleda/shelfkin/api/internal/server"
)

// connectTimeout bounds each new database connection when DATABASE_URL does
// not set connect_timeout, so connections to an unresponsive host do not
// pile up.
const connectTimeout = 5 * time.Second

// poolCloseTimeout bounds how long shutdown waits for the pool to close.
const poolCloseTimeout = time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Getenv, os.Stdout, server.ShutdownTimeout)
	stop()
	os.Exit(code)
}

// run starts the server and blocks until ctx is cancelled (a shutdown
// signal) and the server has stopped. It returns the process exit code.
func run(ctx context.Context, getenv func(string) string, stdout io.Writer, shutdownTimeout time.Duration) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		// Config errors never contain the database password (see config.Error).
		logging.New(stdout, slog.LevelInfo).Error(err.Error())
		return 1
	}
	logger := logging.New(stdout, cfg.LogLevel, cfg.Pool.ConnConfig.Password)
	slog.SetDefault(logger)

	if cfg.Pool.ConnConfig.ConnectTimeout == 0 {
		cfg.Pool.ConnConfig.ConnectTimeout = connectTimeout
	}
	// The pool connects lazily, so the server starts even when the database
	// is unreachable; GET /health then reports 503.
	pool, err := pgxpool.NewWithConfig(ctx, cfg.Pool)
	if err != nil {
		logger.Error("creating database pool", "error", err)
		return 1
	}
	defer closePool(pool)

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.Port))
	if err != nil {
		logger.Error("listening", "port", cfg.Port, "error", err)
		return 1
	}
	logger.Info("listening", "port", cfg.Port)

	srv := server.NewHTTPServer(server.NewHandler(server.NewAPI(pool, logger), logger), logger)
	err = server.Serve(ctx, srv, ln, shutdownTimeout)
	switch {
	case err == nil:
	case errors.Is(err, context.DeadlineExceeded):
		logger.Error("shutdown timed out; in-flight requests were dropped", "timeout", shutdownTimeout.String())
		return 1
	default:
		logger.Error("server failed", "error", err)
		return 1
	}
	closePool(pool)
	logger.Info("stopped")
	return 0
}

// closePool closes pool, giving up after poolCloseTimeout. Closing twice is
// safe.
func closePool(pool *pgxpool.Pool) {
	done := make(chan struct{})
	go func() {
		pool.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(poolCloseTimeout):
	}
}
