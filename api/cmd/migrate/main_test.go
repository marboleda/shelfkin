package main

import (
	"bytes"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
)

// emptyDatabase returns the URL of a fresh test database with every migration
// rolled back, so the migrate command has work to do.
func emptyDatabase(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := db.NewProvider(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(t.Context(), 0); err != nil {
		t.Fatal(err)
	}

	c := pool.Config().ConnConfig
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(int(c.Port))),
		Path:   "/" + c.Database,
	}
	if c.TLSConfig == nil {
		u.RawQuery = "sslmode=disable"
	}
	return u.String(), pool
}

func TestUpAppliesPendingAndIsIdempotent(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()

	var out bytes.Buffer
	if err := run(ctx, []string{"up"}, dbURL, &out); err != nil {
		t.Fatalf("first up: %v", err)
	}
	if !strings.Contains(out.String(), "Applied ") {
		t.Errorf("first up output = %q, want applied migrations", out.String())
	}

	var version int64
	if err := pool.QueryRow(ctx, "SELECT max(version_id) FROM goose_db_version WHERE is_applied").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version == 0 {
		t.Error("no migration recorded as applied after up")
	}

	out.Reset()
	if err := run(ctx, nil, dbURL, &out); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if !strings.Contains(out.String(), "No pending migrations") {
		t.Errorf("second up output = %q, want no-op", out.String())
	}
}

func TestStatusListsMigrations(t *testing.T) {
	t.Parallel()
	dbURL, _ := emptyDatabase(t)
	ctx := t.Context()

	var out bytes.Buffer
	if err := run(ctx, []string{"status"}, dbURL, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "Pending") {
		t.Errorf("status before up = %q, want pending migrations", out.String())
	}

	if err := run(ctx, []string{"up"}, dbURL, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(ctx, []string{"status"}, dbURL, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(out.String(), "Pending") || !strings.Contains(out.String(), "_init.sql") {
		t.Errorf("status after up = %q, want the init migration listed as applied", out.String())
	}
}

func TestDownRollsBackOne(t *testing.T) {
	t.Parallel()
	dbURL, _ := emptyDatabase(t)
	ctx := t.Context()

	if err := run(ctx, []string{"up"}, dbURL, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(ctx, []string{"down"}, dbURL, &out); err != nil {
		t.Fatalf("down: %v", err)
	}
	if !strings.Contains(out.String(), "Rolled back ") {
		t.Errorf("down output = %q", out.String())
	}
}

func TestUnreachableDatabase(t *testing.T) {
	t.Parallel()
	err := run(t.Context(), []string{"up"}, "postgres://nobody:secret@127.0.0.1:1/none?sslmode=disable&connect_timeout=2", &bytes.Buffer{})
	if err == nil {
		t.Fatal("up against an unreachable database succeeded")
	}
	msg := err.Error()
	for _, want := range []string{"could not connect", "127.0.0.1:1", "npm run db:up"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	if strings.Contains(msg, "secret") {
		t.Errorf("error %q leaks the password", msg)
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	err := run(t.Context(), []string{"sideways"}, "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("err = %v, want unknown command", err)
	}
}
