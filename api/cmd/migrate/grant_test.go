package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests create server-wide login roles, so every name is random and
// every role is dropped when the test ends (before its database is).

func randomName(t *testing.T, prefix string) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return prefix + hex.EncodeToString(b)
}

// createRole creates a role with the given attributes and drops it at the
// end of the test.
func createRole(t *testing.T, pool *pgxpool.Pool, prefix, attrs string) string {
	t.Helper()
	name := randomName(t, prefix)
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := pool.Exec(t.Context(), "CREATE ROLE "+ident+" "+attrs); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		// Objects a test made the role own or be granted would block DROP ROLE.
		_, _ = pool.Exec(ctx, "DROP OWNED BY "+ident)
		if _, err := pool.Exec(ctx, "DROP ROLE "+ident); err != nil {
			t.Errorf("dropping role %s: %v", name, err)
		}
	})
	return name
}

// loginURL is dbURL with the user and password replaced.
func loginURL(t *testing.T, dbURL, user, password string) string {
	t.Helper()
	u, err := url.Parse(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

type membership struct {
	role         string
	inherit, set bool
}

func memberships(t *testing.T, pool *pgxpool.Pool, member string) []membership {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT r.rolname, m.inherit_option, m.set_option
		FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid
		WHERE m.member = $1::regrole ORDER BY r.rolname`, pgx.Identifier{member}.Sanitize())
	if err != nil {
		t.Fatal(err)
	}
	ms, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (membership, error) {
		var m membership
		err := row.Scan(&m.role, &m.inherit, &m.set)
		return m, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func wantOnlyAppRole(t *testing.T, pool *pgxpool.Pool, member string) {
	t.Helper()
	ms := memberships(t, pool, member)
	want := []membership{{role: appRole, inherit: false, set: true}}
	if len(ms) != 1 || ms[0] != want[0] {
		t.Errorf("memberships of %s = %+v, want %+v", member, ms, want)
	}
}

func isPermissionDenied(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

func TestUpGrantsAppRoleOnceAndResetRoleHasNoPrivileges(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	api := createRole(t, pool, "e38_api_", "LOGIN PASSWORD 'api-pw' NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS")

	var out bytes.Buffer
	if err := run(ctx, []string{"up"}, dbURL, api, &out); err != nil {
		t.Fatalf("first up: %v", err)
	}
	if !strings.Contains(out.String(), "Granted shelfkin_app to "+api) {
		t.Errorf("first up output = %q, want the grant", out.String())
	}
	wantOnlyAppRole(t, pool, api)

	out.Reset()
	if err := run(ctx, []string{"up"}, dbURL, api, &out); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if s := out.String(); strings.Contains(s, "Granted") || strings.Contains(s, "Updated") || strings.Contains(s, "Ran ") ||
		!strings.Contains(s, "already has shelfkin_app") {
		t.Errorf("second up output = %q, want no changes", s)
	}
	wantOnlyAppRole(t, pool, api)

	// Connect as the login, as the API does.
	conn, err := pgx.Connect(ctx, loginURL(t, dbURL, api, "api-pw"))
	if err != nil {
		t.Fatalf("connecting as %s: %v", api, err)
	}
	defer conn.Close(context.Background())

	// Without SET ROLE (INHERIT FALSE), and after RESET ROLE, it can read
	// nothing.
	for _, table := range []string{"household_members", "users"} {
		if _, err := conn.Exec(ctx, "SELECT FROM "+table); !isPermissionDenied(err) {
			t.Errorf("SELECT from %s without SET ROLE: err = %v, want 42501", table, err)
		}
	}
	for _, table := range []string{"household_members", "users"} {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE shelfkin_app"); err != nil {
			t.Fatalf("SET LOCAL ROLE shelfkin_app: %v", err)
		}
		if _, err := tx.Exec(ctx, "SELECT FROM "+table); err != nil {
			t.Errorf("SELECT from %s as shelfkin_app: %v", table, err)
		}
		if _, err := tx.Exec(ctx, "RESET ROLE"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "SELECT FROM "+table); !isPermissionDenied(err) {
			t.Errorf("SELECT from %s after RESET ROLE: err = %v, want 42501", table, err)
		}
		_ = tx.Rollback(ctx)
	}
}

func TestUpWithoutAPIRoleGrantsNothing(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	api := createRole(t, pool, "e38_api_", "LOGIN")

	var out bytes.Buffer
	if err := run(t.Context(), []string{"up"}, dbURL, "", &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "shelfkin_app") {
		t.Errorf("output = %q, want no grant step", out.String())
	}
	if ms := memberships(t, pool, api); len(ms) != 0 {
		t.Errorf("memberships = %+v, want none", ms)
	}
}

func TestUpFixesAnInheritingGrant(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	api := createRole(t, pool, "e38_api_", "LOGIN")
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "GRANT shelfkin_app TO "+pgx.Identifier{api}.Sanitize()+" WITH INHERIT TRUE"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run(ctx, []string{"up"}, dbURL, api, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Updated the shelfkin_app grant") {
		t.Errorf("output = %q, want the grant updated", out.String())
	}
	wantOnlyAppRole(t, pool, api)
}

// Locally the migrator is a superuser, so it can remove everything; in
// production (Cloud SQL) it may not be, which the next test covers.
func TestUpRemovesExtraMembershipsAndAttributes(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	extra := createRole(t, pool, "e38_extra_", "NOLOGIN")
	api := createRole(t, pool, "e38_api_", "LOGIN CREATEDB CREATEROLE BYPASSRLS")
	if _, err := pool.Exec(ctx, "GRANT "+pgx.Identifier{extra}.Sanitize()+" TO "+pgx.Identifier{api}.Sanitize()); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run(ctx, []string{"up"}, dbURL, api, &out); err != nil {
		t.Fatalf("up: %v\n%s", err, out.String())
	}
	wantOnlyAppRole(t, pool, api)
	var createDB, createRole, bypassRLS bool
	if err := pool.QueryRow(ctx, "SELECT rolcreatedb, rolcreaterole, rolbypassrls FROM pg_roles WHERE rolname = $1", api).
		Scan(&createDB, &createRole, &bypassRLS); err != nil {
		t.Fatal(err)
	}
	if createDB || createRole || bypassRLS {
		t.Errorf("createdb=%v createrole=%v bypassrls=%v, want all false", createDB, createRole, bypassRLS)
	}
	for _, want := range []string{"REVOKE", "NOCREATEDB", "NOCREATEROLE", "NOBYPASSRLS"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output = %q, want it to mention %s", out.String(), want)
		}
	}
}

// A migrator that is not a superuser and has no ADMIN on the extra role (like
// shelfkin_migrator and cloudsqlsuperuser on Cloud SQL) cannot revoke it, so
// the step fails and the deploy stops.
func TestUpFailsWhenAnExtraMembershipCannotBeRevoked(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	extra := createRole(t, pool, "e38_extra_", "NOLOGIN")
	api := createRole(t, pool, "e38_api_", "LOGIN")
	migrator := createRole(t, pool, "e38_mig_", "LOGIN PASSWORD 'mig-pw' CREATEROLE")
	apiIdent := pgx.Identifier{api}.Sanitize()
	if _, err := pool.Exec(ctx, "GRANT "+pgx.Identifier{extra}.Sanitize()+" TO "+apiIdent); err != nil {
		t.Fatal(err)
	}
	// Already granted correctly, so only the extra membership is left to fix.
	if _, err := pool.Exec(ctx, "GRANT shelfkin_app TO "+apiIdent+" WITH INHERIT FALSE, SET TRUE"); err != nil {
		t.Fatal(err)
	}

	sqlDB, err := connect(ctx, loginURL(t, dbURL, migrator, "mig-pw"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	err = grantAppRole(ctx, sqlDB, api, &bytes.Buffer{})
	if err == nil {
		t.Fatal("grant step succeeded with an extra membership left")
	}
	for _, want := range []string{"is a member of " + extra, "One-time admin step", "workflow by hand"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
	if ms := memberships(t, pool, api); len(ms) != 2 {
		t.Errorf("memberships = %+v, want both left in place", ms)
	}
}

func TestUpFailsWhenTheMigratorCannotGrantAppRole(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	api := createRole(t, pool, "e38_api_", "LOGIN")
	migrator := createRole(t, pool, "e38_mig_", "LOGIN PASSWORD 'mig-pw'")

	sqlDB, err := connect(ctx, loginURL(t, dbURL, migrator, "mig-pw"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	err = grantAppRole(ctx, sqlDB, api, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "granting shelfkin_app to "+api) || !isPermissionDenied(err) {
		t.Errorf("err = %v, want permission denied granting shelfkin_app", err)
	}
}

func TestUpFailsWhenTheAPIRoleOwnsOrIsGrantedObjects(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	api := createRole(t, pool, "e38_api_", "LOGIN")
	apiIdent := pgx.Identifier{api}.Sanitize()
	for _, stmt := range []string{
		"CREATE TABLE e38_owned (id int)",
		"ALTER TABLE e38_owned OWNER TO " + apiIdent,
		"GRANT SELECT ON users TO " + apiIdent,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	err := run(ctx, []string{"up"}, dbURL, api, &bytes.Buffer{})
	if err == nil {
		t.Fatal("up succeeded although the API role owns a table and has a grant")
	}
	for _, want := range []string{"owns 1 objects", "1 privileges granted directly"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}

func TestUpFailsForASuperuserAPIRole(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	api := createRole(t, pool, "e38_api_", "LOGIN SUPERUSER")
	err := run(t.Context(), []string{"up"}, dbURL, api, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "has SUPERUSER") {
		t.Errorf("err = %v, want it to refuse a superuser API role", err)
	}
}

func TestUpFailsForAMissingAPIRole(t *testing.T) {
	t.Parallel()
	dbURL, _ := emptyDatabase(t)
	err := run(t.Context(), []string{"up"}, dbURL, "e38_no_such_role", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no such role") {
		t.Errorf("err = %v, want no such role", err)
	}
}
