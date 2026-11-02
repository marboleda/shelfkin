// Command expiry-job sends expiry push notifications once and exits (#22). It
// is meant to run hourly as a Cloud Run job (#40).
//
// Usage:
//
//	go run ./cmd/expiry-job [--now 2026-10-27T13:00:00Z] [--dry-run]
//
// It handles every user with push_enabled and a push token whose local hour
// is 9 at --now (default: the current time), sending one summary push per
// household with items expiring within 3 days. --dry-run logs what would be
// sent and deleted, and sends and writes nothing.
//
// It reads DATABASE_URL (required) and LOG_LEVEL like the API server
// (api/internal/config), plus:
//
//	EXPO_PUSH_URL      the Expo push send endpoint (default
//	                   https://exp.host/--/api/v2/push/send); override it for tests
//	EXPO_ACCESS_TOKEN  optional; sent as a bearer token when set
//
// It logs JSON to stdout, ending with one "expiry job finished" line, and
// exits 1 if any user (or the run itself) failed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/internal/config"
	"github.com/marboleda/shelfkin/api/internal/expiry"
	"github.com/marboleda/shelfkin/api/internal/logging"
)

// Environment variables read by the job in addition to config.Load's.
const (
	ExpoPushURLEnv     = "EXPO_PUSH_URL"
	ExpoAccessTokenEnv = "EXPO_ACCESS_TOKEN"
)

// connectTimeout bounds each new database connection when DATABASE_URL does
// not set connect_timeout.
const connectTimeout = 5 * time.Second

// httpTimeout bounds each request to Expo.
const httpTimeout = 30 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, time.Now)
	stop()
	os.Exit(code)
}

// run runs the job once and returns the process exit code.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clock func() time.Time) int {
	flags := flag.NewFlagSet("expiry-job", flag.ContinueOnError)
	flags.SetOutput(stderr)
	nowFlag := flags.String("now", "", "run as of this time, RFC 3339 (e.g. 2026-10-27T13:00:00Z); default the current time")
	dryRun := flags.Bool("dry-run", false, "log what would be sent and write nothing")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "expiry-job: unexpected arguments %q\n", flags.Args())
		flags.Usage()
		return 2
	}
	now := clock()
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			fmt.Fprintf(stderr, "expiry-job: --now must be an RFC 3339 time like 2026-10-27T13:00:00Z, got %q\n", *nowFlag)
			return 2
		}
		now = t
	}

	cfg, err := config.Load(getenv)
	pushURL := getenv(ExpoPushURLEnv)
	if pushURL == "" {
		pushURL = expiry.DefaultExpoPushURL
	} else if u, perr := url.Parse(pushURL); perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		err = errors.Join(err, fmt.Errorf("%s must be an http(s) URL", ExpoPushURLEnv))
	}
	if err != nil {
		// Config errors never contain the database password (see config.Error).
		logging.New(stdout, slog.LevelInfo).Error(err.Error())
		return 1
	}
	accessToken := getenv(ExpoAccessTokenEnv)
	logger := logging.New(stdout, cfg.LogLevel, cfg.Pool.ConnConfig.Password, accessToken)

	if cfg.Pool.ConnConfig.ConnectTimeout == 0 {
		cfg.Pool.ConnConfig.ConnectTimeout = connectTimeout
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg.Pool)
	if err != nil {
		logger.Error("creating database pool", "error", err)
		return 1
	}
	defer pool.Close()

	job := &expiry.Job{
		DB: pool,
		Sender: &expiry.ExpoClient{
			URL:         pushURL,
			AccessToken: accessToken,
			HTTPClient:  &http.Client{Timeout: httpTimeout},
		},
		Logger: logger,
		DryRun: *dryRun,
	}
	sum, err := job.Run(ctx, now)
	if err != nil || sum.Failures > 0 {
		return 1
	}
	return 0
}
