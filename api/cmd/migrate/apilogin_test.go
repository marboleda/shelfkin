package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// These tests run the API login step (#92) as a Cloud-SQL-like migrator: a
// login with CREATEROLE that is not a superuser and has ADMIN on
// shelfkin_app, as in the grant tests. The superuser pool plays the Cloud SQL
// admin.

const sectionName = "Moving `shelfkin_api` off the Cloud SQL built-in user"

// migratedDatabase is emptyDatabase with every migration applied.
func migratedDatabase(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	dbURL, pool := emptyDatabase(t)
	if err := run(t.Context(), []string{"up"}, dbURL, "", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	return dbURL, pool
}

// cloudSQLMigrator creates the Cloud-SQL-like migrator and returns a
// connection as it.
func cloudSQLMigrator(t *testing.T, dbURL string, pool *pgxpool.Pool) *sql.DB {
	t.Helper()
	migrator := createRole(t, pool, "e92_mig_", "LOGIN PASSWORD 'mig-pw' CREATEROLE")
	if _, err := pool.Exec(t.Context(), "GRANT shelfkin_app TO "+pgx.Identifier{migrator}.Sanitize()+" WITH ADMIN TRUE, INHERIT FALSE, SET FALSE"); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := connect(t.Context(), loginURL(t, dbURL, migrator, "mig-pw"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

// jobRoleName returns a name for a role the job will create, dropped at the
// end of the test. Call it after cloudSQLMigrator, so the role (whose ADMIN
// row names the migrator) is dropped first.
func jobRoleName(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	name := randomName(t, "e92_api_")
	t.Cleanup(func() {
		ctx := context.Background()
		ident := pgx.Identifier{name}.Sanitize()
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)", name).Scan(&exists); err != nil || !exists {
			return
		}
		_, _ = pool.Exec(ctx, "DROP OWNED BY "+ident)
		if _, err := pool.Exec(ctx, "DROP ROLE "+ident); err != nil {
			t.Errorf("dropping role %s: %v", name, err)
		}
	})
	return name
}

// apiStep runs what `migrate up` runs after goose with API_DB_ROLE and
// API_DATABASE_URL set, as the given migrator.
func apiStep(t *testing.T, sqlDB *sql.DB, apiRole, apiURL string) (string, error) {
	t.Helper()
	login, err := parseAPILogin(apiURL, apiRole)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	err = apiRoleStep(t.Context(), sqlDB, apiRole, login, &out)
	return out.String(), err
}

// wantNoLeak fails if the output or the error contains a password, a SCRAM
// verifier or a full URL.
func wantNoLeak(t *testing.T, out string, err error, secrets ...string) {
	t.Helper()
	texts := map[string]string{"output": out}
	if err != nil {
		texts["error"] = err.Error()
	}
	for where, text := range texts {
		for _, s := range append(secrets, "SCRAM-SHA-256$") {
			if strings.Contains(text, s) {
				t.Errorf("%s %q contains %q", where, text, s)
			}
		}
	}
}

func roleExists(t *testing.T, pool *pgxpool.Pool, role string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(t.Context(), "SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)", role).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func rolPassword(t *testing.T, pool *pgxpool.Pool, role string) string {
	t.Helper()
	var pw string
	if err := pool.QueryRow(t.Context(), "SELECT rolpassword FROM pg_authid WHERE rolname = $1", role).Scan(&pw); err != nil {
		t.Fatal(err)
	}
	return pw
}

type fullMembership struct {
	role                string
	inherit, set, admin bool
}

func fullMemberships(t *testing.T, pool *pgxpool.Pool, member string) []fullMembership {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT r.rolname, m.inherit_option, m.set_option, m.admin_option
		FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid
		WHERE m.member = $1::regrole ORDER BY 1`, pgx.Identifier{member}.Sanitize())
	if err != nil {
		t.Fatal(err)
	}
	ms, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (fullMembership, error) {
		var m fullMembership
		return m, row.Scan(&m.role, &m.inherit, &m.set, &m.admin)
	})
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func wantLogin(t *testing.T, dbURL, user, password string) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), loginURL(t, dbURL, user, password))
	if err != nil {
		t.Fatalf("logging in as %s: %v", user, err)
	}
	conn.Close(context.Background())
}

func wantBadPassword(t *testing.T, dbURL, user, password string) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), loginURL(t, dbURL, user, password))
	if err == nil {
		conn.Close(context.Background())
		t.Fatalf("logging in as %s with the old password succeeded", user)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "28P01" {
		t.Errorf("logging in as %s with the old password: err = %v, want 28P01", user, err)
	}
}

func TestAPILoginIsCreatedKeptAndRotated(t *testing.T) {
	t.Parallel()
	dbURL, pool := migratedDatabase(t)
	ctx := t.Context()
	migrator := cloudSQLMigrator(t, dbURL, pool)
	api := jobRoleName(t, pool)
	pw := randomName(t, "pw")
	apiURL := loginURL(t, dbURL, api, pw)

	// Missing role: created, granted shelfkin_app and checked.
	out, err := apiStep(t, migrator, api, apiURL)
	wantNoLeak(t, out, err, pw, apiURL)
	if err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Created login role " + api + "\n",
		"Granted shelfkin_app to " + api + " (INHERIT FALSE, SET TRUE)\n",
		"Checked: " + api + " can log in\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("first run output = %q, want %q", out, want)
		}
	}
	wantLogin(t, dbURL, api, pw)

	var super, createDB, createRole, bypassRLS, replication bool
	if err := pool.QueryRow(ctx, "SELECT rolsuper, rolcreatedb, rolcreaterole, rolbypassrls, rolreplication FROM pg_roles WHERE rolname = $1", api).
		Scan(&super, &createDB, &createRole, &bypassRLS, &replication); err != nil {
		t.Fatal(err)
	}
	if super || createDB || createRole || bypassRLS || replication {
		t.Errorf("attributes super=%v createdb=%v createrole=%v bypassrls=%v replication=%v, want all false",
			super, createDB, createRole, bypassRLS, replication)
	}
	wantMembership := []fullMembership{{role: appRole, inherit: false, set: true, admin: false}}
	if ms := fullMemberships(t, pool, api); len(ms) != 1 || ms[0] != wantMembership[0] {
		t.Errorf("memberships = %+v, want %+v", ms, wantMembership)
	}

	adminDB := stdlib.OpenDBFromPool(pool)
	defer adminDB.Close()
	adminConn, err := adminDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deps, err := roleDependencies(ctx, adminConn, pgx.Identifier{api}.Sanitize())
	adminConn.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 0 {
		t.Errorf("pg_shdepend rows for %s = %v, want none", api, deps)
	}
	wantResetRoleDenied(t, dbURL, api, pw)

	stored := rolPassword(t, pool, api)
	if !strings.HasPrefix(stored, "SCRAM-SHA-256$4096:") {
		t.Errorf("rolpassword starts %.20q, want SCRAM-SHA-256$4096:", stored)
	}

	// Second run: nothing changes.
	out, err = apiStep(t, migrator, api, apiURL)
	wantNoLeak(t, out, err, pw, apiURL)
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if strings.Contains(out, "Created") || strings.Contains(out, "Set a new password") || strings.Contains(out, "Granted") {
		t.Errorf("second run output = %q, want no changes", out)
	}
	for _, want := range []string{api + " can log in; password unchanged", "already has shelfkin_app", "Checked: " + api + " can log in"} {
		if !strings.Contains(out, want) {
			t.Errorf("second run output = %q, want %q", out, want)
		}
	}
	if got := rolPassword(t, pool, api); got != stored {
		t.Error("second run changed the stored password")
	}
	if ms := fullMemberships(t, pool, api); len(ms) != 1 || ms[0] != wantMembership[0] {
		t.Errorf("memberships after the second run = %+v, want %+v", ms, wantMembership)
	}

	// The URL has a new password (terraform apply -replace): the ALTER runs.
	newPW := randomName(t, "pw")
	newURL := loginURL(t, dbURL, api, newPW)
	out, err = apiStep(t, migrator, api, newURL)
	wantNoLeak(t, out, err, pw, newPW, apiURL, newURL)
	if err != nil {
		t.Fatalf("run with a new password: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Set a new password for "+api+"\n") || !strings.Contains(out, "Checked: "+api+" can log in") {
		t.Errorf("output = %q, want the password set and the login checked", out)
	}
	wantLogin(t, dbURL, api, newPW)
	wantBadPassword(t, dbURL, api, pw)
	if got := rolPassword(t, pool, api); !strings.HasPrefix(got, "SCRAM-SHA-256$4096:") || got == stored {
		t.Error("the stored password is not a new SCRAM verifier")
	}
}

// Production today: shelfkin_api was made by Cloud SQL (here: the superuser)
// as a member of cloudsqlsuperuser, granted by an admin, with NOCREATEDB
// NOCREATEROLE set afterwards. The migrator has no ADMIN on it.
func builtInStandIn(t *testing.T, pool *pgxpool.Pool, password string) (api, csu string) {
	t.Helper()
	csu = createRole(t, pool, "e92_csu_", "NOLOGIN CREATEDB CREATEROLE")
	api = createRole(t, pool, "e92_api_", "LOGIN PASSWORD '"+password+"' NOCREATEDB NOCREATEROLE")
	if _, err := pool.Exec(t.Context(), "GRANT "+pgx.Identifier{csu}.Sanitize()+" TO "+pgx.Identifier{api}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	return api, csu
}

func TestAPILoginOnTheBuiltInUserFailsClosedAtTheGrantStep(t *testing.T) {
	t.Parallel()
	dbURL, pool := migratedDatabase(t)
	pw := randomName(t, "pw")
	api, csu := builtInStandIn(t, pool, pw)
	migrator := cloudSQLMigrator(t, dbURL, pool)
	apiURL := loginURL(t, dbURL, api, pw)
	stored := rolPassword(t, pool, api)

	out, err := apiStep(t, migrator, api, apiURL)
	wantNoLeak(t, out, err, pw, apiURL)
	if err == nil {
		t.Fatalf("step passed with a cloudsqlsuperuser membership left; output %q", out)
	}
	if !strings.Contains(out, api+" can log in; password unchanged") {
		t.Errorf("output = %q, want the login accepted unchanged", out)
	}
	for _, want := range []string{"is a member of " + csu + " (granted by", sectionName, "workflow by hand"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
	if strings.Contains(out, "Checked:") {
		t.Errorf("output = %q, want no login check after a failed grant step", out)
	}
	if got := rolPassword(t, pool, api); got != stored {
		t.Error("the password was changed")
	}
}

func TestAPILoginOnTheBuiltInUserWithAWrongPasswordFailsAtAlterRole(t *testing.T) {
	t.Parallel()
	dbURL, pool := migratedDatabase(t)
	pw := randomName(t, "pw")
	api, csu := builtInStandIn(t, pool, pw)
	migrator := cloudSQLMigrator(t, dbURL, pool)
	wrongPW := randomName(t, "pw")
	apiURL := loginURL(t, dbURL, api, wrongPW)

	out, err := apiStep(t, migrator, api, apiURL)
	wantNoLeak(t, out, err, pw, wrongPW, apiURL)
	if err == nil {
		t.Fatalf("step passed although ALTER ROLE must be refused; output %q", out)
	}
	for _, want := range []string{"setting its password failed", "permission denied", sectionName} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
	if strings.Contains(out, "Set a new password") || strings.Contains(out, "Granted") {
		t.Errorf("output = %q, want nothing changed", out)
	}
	if ms := fullMemberships(t, pool, api); len(ms) != 1 || ms[0].role != csu {
		t.Errorf("memberships = %+v, want only the stand-in left", ms)
	}
	wantLogin(t, dbURL, api, pw)
}

// A login failure other than a wrong password stops the job, and the error
// shows the user, host and database but not the password.
func TestAPILoginOtherLoginFailuresStopTheJob(t *testing.T) {
	t.Parallel()
	dbURL, pool := migratedDatabase(t)
	pw := randomName(t, "pw")
	api := createRole(t, pool, "e92_api_", "NOLOGIN PASSWORD '"+pw+"'")
	migrator := cloudSQLMigrator(t, dbURL, pool)
	apiURL := loginURL(t, dbURL, api, pw)

	out, err := apiStep(t, migrator, api, apiURL)
	wantNoLeak(t, out, err, pw, apiURL)
	if err == nil {
		t.Fatalf("step passed for a NOLOGIN role; output %q", out)
	}
	for _, want := range []string{"user=" + api, "host=", "database="} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
	if out != "" {
		t.Errorf("output = %q, want nothing done", out)
	}
}

// Wrong settings stop `migrate up` before it connects: no migration is
// applied and no role is created.
func TestAPILoginSettingsAreCheckedBeforeAnything(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	ctx := t.Context()
	api := randomName(t, "e92_api_")
	pw := randomName(t, "pw")
	apiURL := loginURL(t, dbURL, api, pw)
	// Ends in the password, so a quoted URL fragment would show it.
	badURL := "postgres://" + api + ":" + pw + "@localhost:notaport/db"

	for _, tc := range []struct {
		name, apiRole, apiURL, want string
	}{
		{"user differs from API_DB_ROLE", api + "x", apiURL, APIRoleEnv + "=" + api + "x does not match the user name in " + APIURLEnv},
		{"API_DB_ROLE unset", "", apiURL, APIURLEnv + " is set but " + APIRoleEnv + " is not"},
		{"API_DB_ROLE is shelfkin_app", appRole, loginURL(t, dbURL, appRole, pw), "not shelfkin_app"},
		{"unparsable URL", api, badURL, APIURLEnv + " is not a valid connection string"},
		{"no password", api, loginURL(t, dbURL, api, ""), APIURLEnv + " has no password"},
	} {
		var out bytes.Buffer
		err := runWithAPIURL(ctx, []string{"up"}, dbURL, tc.apiRole, tc.apiURL, &out)
		wantNoLeak(t, out.String(), err, pw, tc.apiURL)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
		if out.Len() != 0 {
			t.Errorf("%s: output = %q, want nothing done", tc.name, out.String())
		}
	}
	if roleExists(t, pool, api) || roleExists(t, pool, api+"x") {
		t.Error("a role was created")
	}
	var applied bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT FROM goose_db_version WHERE is_applied AND version_id > 0)").Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Error("migrations were applied despite the wrong settings")
	}
}

// End to end through run as the local superuser: create, grant and check.
func TestUpWithAPIDatabaseURLCreatesTheLogin(t *testing.T) {
	t.Parallel()
	dbURL, pool := emptyDatabase(t)
	api := jobRoleName(t, pool)
	pw := randomName(t, "pw")
	apiURL := loginURL(t, dbURL, api, pw)

	var out bytes.Buffer
	err := runWithAPIURL(t.Context(), []string{"up"}, dbURL, api, apiURL, &out)
	wantNoLeak(t, out.String(), err, pw, apiURL)
	if err != nil {
		t.Fatalf("up: %v\n%s", err, out.String())
	}
	s := out.String()
	iApplied, iCreated, iGranted, iChecked := strings.Index(s, "Applied "), strings.Index(s, "Created login role "+api),
		strings.Index(s, "Granted shelfkin_app to "+api), strings.Index(s, "Checked: "+api+" can log in")
	if iApplied < 0 || iCreated < iApplied || iGranted < iCreated || iChecked < iGranted {
		t.Errorf("output = %q, want migrations, then the role created, granted and checked, in that order", s)
	}
	wantResetRoleDenied(t, dbURL, api, pw)
}

var b64Part = `[A-Za-z0-9+/]+={0,2}`

func TestScramVerifierFormat(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^SCRAM-SHA-256\$4096:(` + b64Part + `)\$(` + b64Part + `):(` + b64Part + `)$`)
	v1, err := scramVerifier("Passw0rdWithNoSpecials")
	if err != nil {
		t.Fatal(err)
	}
	m := re.FindStringSubmatch(v1)
	if m == nil {
		t.Fatalf("verifier %q has the wrong format", v1)
	}
	for i, wantLen := range map[int]int{1: 16, 2: 32, 3: 32} {
		b, err := base64.StdEncoding.DecodeString(m[i])
		if err != nil || len(b) != wantLen {
			t.Errorf("part %d decodes to %d bytes (err %v), want %d", i, len(b), err, wantLen)
		}
	}
	if strings.Contains(v1, "Passw0rdWithNoSpecials") {
		t.Error("the verifier contains the password")
	}

	v2, err := scramVerifier("Passw0rdWithNoSpecials")
	if err != nil {
		t.Fatal(err)
	}
	if v1 == v2 {
		t.Error("two verifiers of the same password are equal: the salt is not random")
	}

	salt := []byte("0123456789abcdef")
	a, _ := scramVerifierWithSalt("pencil", salt, 4096)
	b, _ := scramVerifierWithSalt("pencil", salt, 4096)
	c, _ := scramVerifierWithSalt("pencil2", salt, 4096)
	if a != b || a == c {
		t.Errorf("verifier is not a function of password and salt: %q %q %q", a, b, c)
	}
}

func TestParseAPILoginRejectsNonASCIIPasswords(t *testing.T) {
	t.Parallel()
	_, err := parseAPILogin("postgres://u:p%C3%A4ss@localhost/db", "u")
	if err == nil || !strings.Contains(err.Error(), "printable ASCII") || strings.Contains(err.Error(), "päss") {
		t.Errorf("err = %v, want a printable ASCII error without the password", err)
	}
}
