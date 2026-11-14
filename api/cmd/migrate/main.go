// Command migrate applies Shelfkin's embedded database migrations.
//
// Usage:
//
//	go run ./cmd/migrate [up|down|status]
//
// It connects using DATABASE_URL, or the local compose database when that is
// unset. When API_DB_ROLE is set, `up` also grants shelfkin_app to that login
// role (see grantAppRole); the deploy workflow sets it to shelfkin_api.
//
// When API_DATABASE_URL (the URL the API connects with) is also set, `up`
// first creates the API_DB_ROLE login if it is missing, or sets its password
// if the URL cannot log in, sending only a SCRAM-SHA-256 verifier (see
// ensureAPILogin). After the grant step it logs in with the URL once more and
// checks that it connects as API_DB_ROLE. API_DATABASE_URL needs API_DB_ROLE
// set to the URL's user name. Neither the URL nor its password is ever
// printed.
//
// From the repository root, use `npm run db:migrate` and `npm run db:status`;
// neither sets API_DB_ROLE or API_DATABASE_URL.
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

  up      apply all pending migrations (default), then, when API_DB_ROLE is
          set, grant shelfkin_app to that login role
  down    roll back the most recent migration
  status  list migrations and whether they are applied

Connects using DATABASE_URL, or the local compose database when it is unset.

When API_DATABASE_URL (the API's connection string) is set, up also creates
the API_DB_ROLE login if it is missing, or sets its password if the URL cannot
log in (as a SCRAM verifier only), and after the grant step checks that the
URL logs in as API_DB_ROLE. API_DB_ROLE must then be the URL's user name.`

func main() {
	ctx := context.Background()
	if err := runWithAPIURL(ctx, os.Args[1:], os.Getenv("DATABASE_URL"), os.Getenv(APIRoleEnv), os.Getenv(APIURLEnv), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

// run executes one migrate command against the database at databaseURL. An
// empty databaseURL means the local compose database. A non-empty apiRole
// makes `up` grant shelfkin_app to that role after migrating.
func run(ctx context.Context, args []string, databaseURL, apiRole string, out io.Writer) error {
	return runWithAPIURL(ctx, args, databaseURL, apiRole, "", out)
}

// runWithAPIURL is run with API_DATABASE_URL: a non-empty apiURL makes `up`
// create or update the apiRole login before the grant step and check that it
// can log in after it.
func runWithAPIURL(ctx context.Context, args []string, databaseURL, apiRole, apiURL string, out io.Writer) error {
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

	// Checked before connecting, so a wrong setting changes nothing.
	var login *apiLogin
	if cmd == "up" && apiURL != "" {
		var err error
		if login, err = parseAPILogin(apiURL, apiRole); err != nil {
			return err
		}
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
		if err := apiRoleStep(ctx, sqlDB, apiRole, login, out); err != nil {
			return err
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

// apiRoleStep runs after the migrations: with a login (API_DATABASE_URL), it
// creates or updates the API login role, then the grant step runs when
// apiRole is set, then the login is checked.
func apiRoleStep(ctx context.Context, sqlDB *sql.DB, apiRole string, login *apiLogin, out io.Writer) error {
	if login != nil {
		if err := ensureAPILogin(ctx, sqlDB, login, out); err != nil {
			return err
		}
	}
	if apiRole != "" {
		if err := grantAppRole(ctx, sqlDB, apiRole, out); err != nil {
			return err
		}
	}
	if login != nil {
		return verifyAPILogin(ctx, login, apiRole, out)
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
