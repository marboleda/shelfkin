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

// wantResetRoleDenied connects as the login and checks that, without SET ROLE
// and after SET LOCAL ROLE shelfkin_app; RESET ROLE, it can read neither
// household_members nor users.
func wantResetRoleDenied(t *testing.T, dbURL, login, password string) {
	t.Helper()
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, loginURL(t, dbURL, login, password))
	if err != nil {
		t.Fatalf("connecting as %s: %v", login, err)
	}
	defer conn.Close(context.Background())
	for _, table := range []string{"household_members", "users"} {
		if _, err := conn.Exec(ctx, "SELECT FROM "+table); !isPermissionDenied(err) {
			t.Errorf("SELECT from %s without SET ROLE: err = %v, want 42501", table, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range []string{"SET LOCAL ROLE shelfkin_app", "RESET ROLE"} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		if _, err := tx.Exec(ctx, "SELECT FROM "+table); !isPermissionDenied(err) {
			t.Errorf("SELECT from %s after RESET ROLE: err = %v, want 42501", table, err)
		}
		_ = tx.Rollback(ctx)
	}
}

// QA of #38: on Postgres 16+ each grantor has its own pg_auth_members row, and
// GRANT only updates the runner's row. An INHERIT TRUE grant made by another
// role (an admin) must be revoked as granted by that role, not left in place.
func TestUpRevokesAnInheritingGrantMadeByAnotherGrantor(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// Created before the API role, so it is dropped after it (its grant goes
	// with the API role).
	adm := createRole(t, pool, "e38_adm_", "NOLOGIN")
	api := createRole(t, pool, "e38_api_", "LOGIN PASSWORD 'api-pw'")
	apiIdent, admIdent := pgx.Identifier{api}.Sanitize(), pgx.Identifier{adm}.Sanitize()
	for _, stmt := range []string{
		"GRANT shelfkin_app TO " + admIdent + " WITH ADMIN TRUE",
		"GRANT shelfkin_app TO " + apiIdent + " WITH INHERIT FALSE, SET TRUE",
		"GRANT shelfkin_app TO " + apiIdent + " WITH INHERIT TRUE GRANTED BY " + admIdent,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if ms := memberships(t, pool, api); len(ms) != 2 {
		t.Fatalf("memberships = %+v, want the right grant and the admin's inheriting one", ms)
	}

	var out bytes.Buffer
	if err := run(ctx, []string{"up"}, dbURL, api, &out); err != nil {
		t.Fatalf("up: %v\n%s", err, out.String())
	}
	if want := "REVOKE shelfkin_app FROM " + apiIdent + " GRANTED BY " + admIdent; !strings.Contains(out.String(), want) {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
	wantOnlyAppRole(t, pool, api)
	wantResetRoleDenied(t, dbURL, api, "api-pw")
}

// The same, but the runner is not a superuser (shelfkin_migrator on Cloud
// SQL): it cannot revoke another role's grant, so the check must fail closed
// instead of reporting the grant as fixed.
func TestUpFailsOnAnInheritingGrantItCannotRevoke(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	api := createRole(t, pool, "e38_api_", "LOGIN PASSWORD 'api-pw'")
	migrator := createRole(t, pool, "e38_mig_", "LOGIN PASSWORD 'mig-pw' CREATEROLE")
	apiIdent := pgx.Identifier{api}.Sanitize()
	// The migrator has ADMIN on shelfkin_app, as the creator does in production.
	if _, err := pool.Exec(ctx, "GRANT shelfkin_app TO "+pgx.Identifier{migrator}.Sanitize()+" WITH ADMIN TRUE, INHERIT FALSE, SET FALSE"); err != nil {
		t.Fatal(err)
	}

	sqlDB, err := connect(ctx, loginURL(t, dbURL, migrator, "mig-pw"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var out bytes.Buffer
	if err := grantAppRole(ctx, sqlDB, api, &out); err != nil {
		t.Fatalf("first grant step: %v", err)
	}

	// An admin adds an inheriting grant next to the migrator's right one.
	var admin string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "GRANT shelfkin_app TO "+apiIdent+" WITH INHERIT TRUE"); err != nil {
		t.Fatal(err)
	}

	for range 2 { // fails the same way every run
		out.Reset()
		err = grantAppRole(ctx, sqlDB, api, &out)
		if err == nil {
			t.Fatalf("grant step succeeded with an inheriting grant by %s left; output %q", admin, out.String())
		}
		for _, want := range []string{
			"has a wrong grant of shelfkin_app (granted by " + admin + " with INHERIT TRUE",
			"One-time admin step",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q, want it to mention %q", err, want)
			}
		}
		if strings.Contains(out.String(), "Updated") || strings.Contains(out.String(), "Ran ") {
			t.Errorf("output = %q, want no claimed fix", out.String())
		}
	}
	if ms := memberships(t, pool, api); len(ms) != 2 {
		t.Errorf("memberships = %+v, want both rows left for the admin", ms)
	}
}

// A grant with ADMIN would let the login grant shelfkin_app onwards: the
// option is revoked and the membership kept.
func TestUpRevokesAdminOptionOnTheAppGrant(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	api := createRole(t, pool, "e38_api_", "LOGIN")
	if _, err := pool.Exec(ctx, "GRANT shelfkin_app TO "+pgx.Identifier{api}.Sanitize()+" WITH ADMIN TRUE, INHERIT FALSE, SET TRUE"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(ctx, []string{"up"}, dbURL, api, &out); err != nil {
		t.Fatalf("up: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "REVOKE ADMIN OPTION FOR shelfkin_app") {
		t.Errorf("output = %q, want the ADMIN option revoked", out.String())
	}
	wantOnlyAppRole(t, pool, api)
	var adminOpt bool
	if err := pool.QueryRow(ctx, "SELECT bool_or(admin_option) FROM pg_auth_members WHERE member = $1::regrole",
		pgx.Identifier{api}.Sanitize()).Scan(&adminOpt); err != nil {
		t.Fatal(err)
	}
	if adminOpt {
		t.Error("shelfkin_app grant still has ADMIN")
	}
}

// Locally the migrator is a superuser, so it can remove everything; in
// production (Cloud SQL) it may not be, which the next test covers.
func TestUpRemovesExtraMembershipsAndAttributes(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	extra := createRole(t, pool, "e38_extra_", "NOLOGIN")
	api := createRole(t, pool, "e38_api_", "LOGIN CREATEDB CREATEROLE BYPASSRLS REPLICATION")
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
	for _, want := range []string{"REVOKE", "NOCREATEDB", "NOCREATEROLE", "NOBYPASSRLS", "NOREPLICATION"} {
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
	api := createRole(t, pool, "e38_api_", "LOGIN REPLICATION")
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
	var admin string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&admin); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"is a member of " + extra + " (granted by " + admin, "has REPLICATION", "One-time admin step", "workflow by hand"} {
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
	for _, want := range []string{"owns 1 objects (table e38_owned)", "privileges granted directly to it on 1 objects (table users)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}

// Re-QA of #38: column privileges live in pg_attribute.attacl, not relacl. A
// column grant on users lets the login read emails after RESET ROLE (users
// has no RLS), so it must fail the step and be named.
func TestUpFailsOnAColumnGrant(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	api := createRole(t, pool, "e38_api_", "LOGIN PASSWORD 'api-pw'")
	if _, err := pool.Exec(ctx, "GRANT SELECT (id, email) ON users TO "+pgx.Identifier{api}.Sanitize()); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := run(ctx, []string{"up"}, dbURL, api, &out)
	if err == nil {
		t.Fatalf("up succeeded with a column grant on users; output %q", out.String())
	}
	for _, want := range []string{
		"privileges granted directly to it on 2 objects",
		"column email of table users",
		"column id of table users",
		"One-time admin step",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}

	// The README's production check (a zero-column SELECT after RESET ROLE,
	// which must fail) catches it too: with any column privilege it succeeds.
	conn, err := pgx.Connect(ctx, loginURL(t, dbURL, api, "api-pw"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "SELECT FROM users"); err != nil {
		t.Errorf("zero-column SELECT FROM users with a column grant: err = %v, want success (so the README check fails)", err)
	}
}

// Direct grants in every other ACL catalog are found and named too.
func TestUpFailsOnGrantsInOtherCatalogs(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	api := createRole(t, pool, "e38_api_", "LOGIN")
	apiIdent := pgx.Identifier{api}.Sanitize()
	var dbName, loid string
	if err := pool.QueryRow(ctx, "SELECT current_database(), lo_create(0)::text").Scan(&dbName, &loid); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"CREATE DOMAIN e38_dom AS int",
		"GRANT USAGE ON DOMAIN e38_dom TO " + apiIdent,
		"GRANT SELECT ON LARGE OBJECT " + loid + " TO " + apiIdent,
		"GRANT USAGE ON LANGUAGE plpgsql TO " + apiIdent,
		"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO " + apiIdent,
		"GRANT CREATE ON DATABASE " + pgx.Identifier{dbName}.Sanitize() + " TO " + apiIdent,
		"GRANT SET ON PARAMETER work_mem TO " + apiIdent,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	err := run(ctx, []string{"up"}, dbURL, api, &bytes.Buffer{})
	if err == nil {
		t.Fatal("up succeeded with direct grants in other catalogs")
	}
	for _, want := range []string{
		"privileges granted directly to it on 6 objects",
		"type e38_dom",
		"large object " + loid,
		"language plpgsql",
		"default privileges on new relations belonging to role",
		"database " + dbName,
		"parameter work_mem",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}

// Third QA of #38: ownership is read from pg_shdepend, so every kind of
// owned object is found, including a domain over an array (typcategory 'A',
// which the old per-catalog count skipped) and kinds it never looked at.
func TestUpFailsOnAnyOwnedObject(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	api := createRole(t, pool, "e38_api_", "LOGIN")
	apiIdent := pgx.Identifier{api}.Sanitize()
	for _, stmt := range []string{
		"CREATE DOMAIN e38_intarr AS int[]",
		"ALTER DOMAIN e38_intarr OWNER TO " + apiIdent,
		"CREATE STATISTICS e38_stats ON id, email FROM users",
		"ALTER STATISTICS e38_stats OWNER TO " + apiIdent,
		`CREATE COLLATION e38_coll FROM "C"`,
		"ALTER COLLATION e38_coll OWNER TO " + apiIdent,
		"CREATE PUBLICATION e38_pub",
		"ALTER PUBLICATION e38_pub OWNER TO " + apiIdent,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	err := run(ctx, []string{"up"}, dbURL, api, &bytes.Buffer{})
	if err == nil {
		t.Fatal("up succeeded although the API role owns objects")
	}
	for _, want := range []string{
		"owns 4 objects",
		"type e38_intarr",
		"statistics object e38_stats",
		"collation e38_coll",
		"publication e38_pub",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}

// A grant on a tablespace (a shared object, outside this database's
// catalogs) is found too, while CONNECT and TEMPORARY on a database are not
// counted.
func TestUpFailsOnATablespaceGrantButAllowsConnect(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	if err := run(ctx, []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var dbName string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	api := createRole(t, pool, "e38_api_", "LOGIN")
	apiIdent := pgx.Identifier{api}.Sanitize()
	if _, err := pool.Exec(ctx, "GRANT CONNECT, TEMPORARY ON DATABASE "+pgx.Identifier{dbName}.Sanitize()+" TO "+apiIdent); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"up"}, dbURL, api, &bytes.Buffer{}); err != nil {
		t.Fatalf("up with only CONNECT and TEMPORARY on the database: %v", err)
	}

	// An in-place tablespace needs no directory on the server. Tablespaces are
	// shared, so it is dropped after the role (cleanups run last first).
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tsName := randomName(t, "e38_ts_")
	tsIdent := pgx.Identifier{tsName}.Sanitize()
	_, err = conn.Exec(ctx, "SET allow_in_place_tablespaces = on")
	if err == nil {
		_, err = conn.Exec(ctx, "CREATE TABLESPACE "+tsIdent+" LOCATION ''")
	}
	conn.Release()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP TABLESPACE "+tsIdent); err != nil {
			t.Errorf("dropping tablespace: %v", err)
		}
	})
	// Registered after the tablespace cleanup, so it runs first.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "REVOKE ALL ON TABLESPACE "+tsIdent+" FROM "+apiIdent)
	})
	if _, err := pool.Exec(ctx, "GRANT CREATE ON TABLESPACE "+tsIdent+" TO "+apiIdent); err != nil {
		t.Fatal(err)
	}

	err = run(ctx, []string{"up"}, dbURL, api, &bytes.Buffer{})
	if err == nil {
		t.Fatal("up succeeded with a tablespace grant")
	}
	if want := "privileges granted directly to it on 1 objects (tablespace " + tsName + ")"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to mention %q", err, want)
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
