// Package dbtest gives each Go test its own fresh, fully migrated Postgres
// database.
//
// It connects to the server named by TEST_DATABASE_URL, or to the local
// compose database (`npm run db:up`) when that is unset. If the server cannot
// be reached the test fails; it never skips.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
)

// URLEnv names the environment variable holding the connection string of the
// Postgres server that test databases are created on. The role it connects as
// must be allowed to create databases.
const URLEnv = "TEST_DATABASE_URL"

// NamePrefix starts the name of every database this package creates.
const NamePrefix = "shelfkin_test_"

// poolCloseTimeout bounds how long cleanup waits for the pool to close. Closing
// blocks while a connection is still acquired (for example rows that were
// never closed); the database is force-dropped regardless.
const poolCloseTimeout = 2 * time.Second

// New creates a database with a unique name, applies every migration to it and
// returns a connection pool for it. The pool is closed and the database is
// dropped when the test and its subtests finish, whether or not they passed.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	serverURL := os.Getenv(URLEnv)
	source := URLEnv
	if serverURL == "" {
		serverURL = db.LocalURL()
		source = "the local compose database"
	}

	admin, err := connectAdmin(ctx, serverURL)
	if err != nil {
		t.Fatalf("dbtest: could not connect to Postgres (%s): %v\n"+
			"Start the local database with `npm run db:up`, or set %s.", source, err, URLEnv)
	}
	name := NamePrefix + randomSuffix(t)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	admin.Close(ctx)
	if err != nil {
		t.Fatalf("dbtest: creating database %s: %v", name, err)
	}

	// pool is set below; the cleanup closes it (if any) and then drops the
	// database. Registering it right after CREATE DATABASE means the database
	// is dropped even if migrating or connecting fails.
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			closePool(pool)
		}
		if err := dropDatabase(serverURL, name); err != nil {
			t.Errorf("dbtest: dropping database %s: %v", name, err)
		}
	})

	cfg, err := pgxpool.ParseConfig(serverURL)
	if err != nil {
		t.Fatalf("dbtest: parsing %s: %v", source, err)
	}
	cfg.ConnConfig.Database = name

	if err := migrate(ctx, cfg.ConnConfig); err != nil {
		t.Fatalf("dbtest: migrating database %s: %v", name, err)
	}

	pool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("dbtest: opening pool for %s: %v", name, err)
	}
	return pool
}

func connectAdmin(ctx context.Context, serverURL string) (*pgx.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return pgx.Connect(ctx, serverURL)
}

func randomSuffix(t testing.TB) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("dbtest: generating database name: %v", err)
	}
	return hex.EncodeToString(b)
}

func migrate(ctx context.Context, connCfg *pgx.ConnConfig) error {
	sqlDB := stdlib.OpenDB(*connCfg)
	defer sqlDB.Close()
	provider, err := db.NewProvider(sqlDB)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

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

// dropDatabase uses its own context because the test's context is already
// cancelled when cleanup runs. WITH (FORCE) terminates any connections still
// open to the database.
func dropDatabase(serverURL, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, serverURL)
	if err != nil {
		return err
	}
	defer admin.Close(ctx)
	_, err = admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pgx.Identifier{name}.Sanitize()))
	return err
}
