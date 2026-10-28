package dbtest_test

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
)

// helperEnv switches TestHelperProcess on when this test binary is re-run as
// a child process by the tests below.
const helperEnv = "DBTEST_HELPER_PROCESS"

func TestNewAppliesMigrations(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := db.NewProvider(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	sources := provider.ListSources()
	first, latest := sources[0].Version, sources[len(sources)-1].Version

	var applied bool
	err = pool.QueryRow(t.Context(),
		"SELECT EXISTS (SELECT 1 FROM goose_db_version WHERE version_id = $1 AND is_applied)", first,
	).Scan(&applied)
	if err != nil {
		t.Fatalf("querying goose_db_version: %v", err)
	}
	if !applied {
		t.Errorf("first migration %d is not recorded as applied", first)
	}

	var current int64
	if err := pool.QueryRow(t.Context(), "SELECT max(version_id) FROM goose_db_version").Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != latest {
		t.Errorf("database version = %d, want latest migration %d", current, latest)
	}
}

func TestNewUsesUniquePrefixedName(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	name := currentDatabase(t, pool.QueryRow)
	if !strings.HasPrefix(name, dbtest.NamePrefix) {
		t.Errorf("database name %q lacks prefix %q", name, dbtest.NamePrefix)
	}
}

func TestNewParallelTestsAreIsolated(t *testing.T) {
	var mu sync.Mutex
	names := map[string]string{}

	t.Run("group", func(t *testing.T) {
		for _, sub := range []string{"a", "b"} {
			t.Run(sub, func(t *testing.T) {
				t.Parallel()
				ctx := t.Context()
				pool := dbtest.New(t)

				// The same table name in both tests: this would collide if
				// they shared a database.
				if _, err := pool.Exec(ctx, "CREATE TABLE isolation_check (owner text NOT NULL)"); err != nil {
					t.Fatalf("create table: %v", err)
				}
				if _, err := pool.Exec(ctx, "INSERT INTO isolation_check VALUES ($1)", sub); err != nil {
					t.Fatalf("insert: %v", err)
				}
				var owners []string
				rows, err := pool.Query(ctx, "SELECT owner FROM isolation_check")
				if err != nil {
					t.Fatal(err)
				}
				owners, err = pgx.CollectRows(rows, pgx.RowTo[string])
				if err != nil {
					t.Fatal(err)
				}
				if len(owners) != 1 || owners[0] != sub {
					t.Errorf("rows = %v, want only [%s]", owners, sub)
				}

				mu.Lock()
				names[sub] = currentDatabase(t, pool.QueryRow)
				mu.Unlock()
			})
		}
	})

	if names["a"] == "" || names["a"] == names["b"] {
		t.Errorf("parallel tests got databases %q and %q, want two distinct ones", names["a"], names["b"])
	}
	for _, name := range names {
		if databaseExists(t, name) {
			t.Errorf("database %s still exists after its test finished", name)
		}
	}
}

// TestCleanupDropsDatabaseWhenTestFails runs a child test that creates a
// database, leaves connections open (an acquired pool connection and a
// separate connection) and then fails. The database must still be dropped.
func TestCleanupDropsDatabaseWhenTestFails(t *testing.T) {
	out, err := runHelper(t, "fail-with-open-conns", os.Getenv(dbtest.URLEnv))
	if err == nil {
		t.Fatalf("child test passed, want it to fail; output:\n%s", out)
	}
	m := regexp.MustCompile(`created (` + dbtest.NamePrefix + `[0-9a-f]+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("child did not report its database; output:\n%s", out)
	}
	if databaseExists(t, m[1]) {
		t.Errorf("database %s was not dropped after the failing test; output:\n%s", m[1], out)
	}
}

// TestNewFailsWhenServerUnreachable checks that New fails the test, rather
// than skipping it, and tells the developer how to start the database.
func TestNewFailsWhenServerUnreachable(t *testing.T) {
	out, err := runHelper(t, "unreachable", "postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable&connect_timeout=2")
	if err == nil {
		t.Fatalf("child test passed, want it to fail; output:\n%s", out)
	}
	if !strings.Contains(out, "--- FAIL: TestHelperProcess") || strings.Contains(out, "--- SKIP") {
		t.Errorf("child test did not fail cleanly; output:\n%s", out)
	}
	if !strings.Contains(out, "could not connect") || !strings.Contains(out, "npm run db:up") {
		t.Errorf("failure message does not mention connecting and `npm run db:up`; output:\n%s", out)
	}
}

// TestHelperProcess is only active when re-run by runHelper.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv(helperEnv) {
	case "fail-with-open-conns":
		pool := dbtest.New(t)
		name := currentDatabase(t, pool.QueryRow)
		t.Logf("created %s", name)

		// Never released, so pool.Close would block.
		if _, err := pool.Acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		cfg := pool.Config().ConnConfig.Copy()
		conn, err := pgx.ConnectConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn // deliberately left open
		t.Fatal("failing on purpose")
	case "unreachable":
		dbtest.New(t)
	}
}

func runHelper(t *testing.T, mode, testDatabaseURL string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), helperEnv+"="+mode, dbtest.URLEnv+"="+testDatabaseURL)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type queryRower func(ctx context.Context, sql string, args ...any) pgx.Row

func currentDatabase(t *testing.T, queryRow queryRower) string {
	t.Helper()
	var name string
	if err := queryRow(t.Context(), "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

func databaseExists(t *testing.T, name string) bool {
	t.Helper()
	serverURL := os.Getenv(dbtest.URLEnv)
	if serverURL == "" {
		serverURL = db.LocalURL()
	}
	conn, err := pgx.Connect(t.Context(), serverURL)
	if err != nil {
		t.Fatalf("connecting to check databases: %v", err)
	}
	defer conn.Close(context.Background())
	var exists bool
	if err := conn.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}
