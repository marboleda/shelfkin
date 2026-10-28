// Command migrate applies Shelfkin's embedded database migrations.
//
// Usage:
//
//	go run ./cmd/migrate [up|down|status]
//
// It connects using DATABASE_URL, or the local compose database when that is
// unset. From the repository root, use `npm run db:migrate` and
// `npm run db:status`.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/marboleda/shelfkin/api/db"
)

const usage = `usage: migrate [up|down|status]

  up      apply all pending migrations (default)
  down    roll back the most recent migration
  status  list migrations and whether they are applied

Connects using DATABASE_URL, or the local compose database when it is unset.`

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Args[1:], os.Getenv("DATABASE_URL"), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

// run executes one migrate command against the database at databaseURL. An
// empty databaseURL means the local compose database.
func run(ctx context.Context, args []string, databaseURL string, out io.Writer) error {
	cmd := "up"
	switch len(args) {
	case 0:
	case 1:
		cmd = args[0]
	default:
		return errors.New(usage)
	}
	if cmd != "up" && cmd != "down" && cmd != "status" {
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}

	if databaseURL == "" {
		databaseURL = db.LocalURL()
	}
	sqlDB, err := connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	provider, err := db.NewProvider(sqlDB)
	if err != nil {
		return fmt.Errorf("loading migrations: %w", err)
	}

	switch cmd {
	case "up":
		results, err := provider.Up(ctx)
		if err != nil {
			return fmt.Errorf("applying migrations: %w", err)
		}
		if len(results) == 0 {
			fmt.Fprintln(out, "No pending migrations; the database is up to date.")
		}
		for _, r := range results {
			fmt.Fprintf(out, "Applied %s (%s)\n", r.Source.Path, r.Duration.Round(time.Millisecond))
		}
	case "down":
		r, err := provider.Down(ctx)
		if errors.Is(err, goose.ErrNoNextVersion) {
			fmt.Fprintln(out, "No applied migrations to roll back.")
			return nil
		}
		if err != nil {
			return fmt.Errorf("rolling back: %w", err)
		}
		fmt.Fprintf(out, "Rolled back %s (%s)\n", r.Source.Path, r.Duration.Round(time.Millisecond))
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("reading migration status: %w", err)
		}
		fmt.Fprintf(out, "%-25s %s\n", "Applied at", "Migration")
		for _, s := range statuses {
			applied := "Pending"
			if s.State == goose.StateApplied {
				applied = s.AppliedAt.UTC().Format("2006-01-02 15:04:05 UTC")
			}
			fmt.Fprintf(out, "%-25s %s\n", applied, s.Source.Path)
		}
	}
	return nil
}

// connect opens databaseURL and checks that the server is reachable, so a
// stopped database gives a clear error instead of a failure deep inside goose.
func connect(ctx context.Context, databaseURL string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid DATABASE_URL: %w", err)
	}
	sqlDB := stdlib.OpenDB(*cfg)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("could not connect to the database at %s:%d/%s: %w\n"+
			"Is it running? Start the local database with `npm run db:up`, "+
			"or set DATABASE_URL to point at another database.",
			cfg.Host, cfg.Port, cfg.Database, err)
	}
	return sqlDB, nil
}
