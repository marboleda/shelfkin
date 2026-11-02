package db_test

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
)

// rlsMigration is the file that creates the shelfkin_app role, its grants,
// the policy helper functions and the row-level security policies.
const rlsMigration = "20261103000000_rls.sql"

// sqlStateInsufficientPrivilege is raised both for a missing grant
// ("permission denied for table ...") and for a row rejected by a policy
// ("new row violates row-level security policy ...").
const sqlStateInsufficientPrivilege = "42501"

// rlsTables are the tables with row-level security, each with the column the
// policies scope it by.
var rlsTables = map[string]string{
	"households":              "id",
	"household_members":       "household_id",
	"household_invites":       "household_id",
	"catalog_items":           "household_id",
	"household_item_defaults": "household_id",
	"pantry_entries":          "household_id",
	"item_events":             "household_id",
}

// householdRelations are the RLS tables plus the effective_catalog_items view,
// which is security_invoker and so filtered by the policies underneath it.
var householdRelations = func() map[string]string {
	m := map[string]string{"effective_catalog_items": "household_id"}
	for k, v := range rlsTables {
		m[k] = v
	}
	return m
}()

// noRLSAllowlist are the public tables that deliberately have no row-level
// security: they hold no household data. Adding a table to this list is a
// decision; see docs/data-model.md (Access control).
var noRLSAllowlist = map[string]bool{
	"users":                true,
	"push_tokens":          true,
	"expiry_notifications": true,
	"categories":           true,
	"locations":            true,
	"goose_db_version":     true,
}

// rlsHousehold holds the IDs of one household's rows in the fixture.
type rlsHousehold struct {
	id, item, entry, event string
}

// rlsFixture is two households, A and B, each with a custom item, an
// override for a built-in item, an entry, an event and an invite:
//
//   - ownerA owns A; memberA and member2A are plain members of A
//   - ownerB owns B
//   - outsider is in neither household
//
// ownerB also has a push token and an expiry notification.
type rlsFixture struct {
	pool                                        *pgxpool.Pool
	ownerA, memberA, member2A, ownerB, outsider string
	a, b                                        rlsHousehold
	builtIn, builtIn2                           string // built-in items; builtIn has an override in A and B
	builtIns                                    int    // number of built-in items
}

func newRLSFixture(t *testing.T) *rlsFixture {
	t.Helper()
	ctx := t.Context()
	pool := dbtest.New(t)
	f := &rlsFixture{pool: pool}

	f.ownerA = insertUser(t, pool, "fb-owner-a")
	f.memberA = insertUser(t, pool, "fb-member-a")
	f.member2A = insertUser(t, pool, "fb-member2-a")
	f.ownerB = insertUser(t, pool, "fb-owner-b")
	f.outsider = insertUser(t, pool, "fb-outsider")

	rows, err := pool.Query(ctx, `SELECT id::text FROM catalog_items WHERE household_id IS NULL ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	builtIns, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(builtIns) < 2 {
		t.Fatalf("%d seeded built-in items, want at least 2", len(builtIns))
	}
	f.builtIns, f.builtIn, f.builtIn2 = len(builtIns), builtIns[0], builtIns[1]

	setup := func(name, code, owner string) rlsHousehold {
		h := rlsHousehold{id: insertHousehold(t, pool, name, owner)}
		mustInsertMember(t, pool, h.id, owner, "owner")
		h.item = mustAddItem(t, pool, h.id, name+" chutney", catBaking, locPantry, nil)
		setOverride(t, pool, h.id, f.builtIn, catProduce, locFreezer)
		h.entry = insertEntry(t, pool, h.id, h.item, owner)
		h.event = insertEvent(t, pool, h.id, h.entry, h.item, owner, "entry_created")
		if err := insertInvite(ctx, pool, h.id, code, owner); err != nil {
			t.Fatalf("inserting invite: %v", err)
		}
		return h
	}
	f.a = setup("A", "AAAA2345", f.ownerA)
	f.b = setup("B", "BBBB2345", f.ownerB)
	mustInsertMember(t, pool, f.a.id, f.memberA, "member")
	mustInsertMember(t, pool, f.a.id, f.member2A, "member")

	if err := insertToken(ctx, pool, f.ownerB, "ExponentPushToken[b]"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO expiry_notifications (entry_id, user_id, expires_on) VALUES ($1, $2, CURRENT_DATE)`,
		f.b.entry, f.ownerB); err != nil {
		t.Fatal(err)
	}
	return f
}

func pgUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("parsing UUID %q: %v", s, err)
	}
	return u
}

var errRollback = errors.New("rollback")

// inTx runs fn through dbtx as user (no user if user is "") and always rolls
// back, so each call starts from the fixture. It returns fn's error.
func inTx(t *testing.T, pool *pgxpool.Pool, user string, fn func(pgx.Tx) error) error {
	t.Helper()
	var fnErr error
	wrapped := func(tx pgx.Tx) error {
		fnErr = fn(tx)
		return errRollback
	}
	var err error
	if user == "" {
		err = dbtx.WithoutUser(t.Context(), pool, wrapped)
	} else {
		err = dbtx.WithUser(t.Context(), pool, pgUUID(t, user), wrapped)
	}
	if !errors.Is(err, errRollback) {
		t.Fatalf("transaction: %v", err)
	}
	return fnErr
}

// execAs runs one statement as user and rolls it back.
func execAs(t *testing.T, pool *pgxpool.Pool, user, sql string, args ...any) (int64, error) {
	t.Helper()
	var n int64
	err := inTx(t, pool, user, func(tx pgx.Tx) error {
		tag, err := tx.Exec(t.Context(), sql, args...)
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

// countAs runs a count(*) query as user.
func countAs(t *testing.T, pool *pgxpool.Pool, user, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := inTx(t, pool, user, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), sql, args...).Scan(&n)
	}); err != nil {
		t.Fatalf("%s as %q: %v", sql, user, err)
	}
	return n
}

// wantDenied reports an error unless err is SQLSTATE 42501 with a message
// containing msg ("row-level security" or "permission denied").
func wantDenied(t *testing.T, err error, msg, what string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Errorf("%s: err = %v, want SQLSTATE %s (%s)", what, err, sqlStateInsufficientPrivilege, msg)
		return
	}
	if pgErr.Code != sqlStateInsufficientPrivilege || !strings.Contains(pgErr.Message, msg) {
		t.Errorf("%s: SQLSTATE %s (%s), want %s (%s)", what, pgErr.Code, pgErr.Message, sqlStateInsufficientPrivilege, msg)
	}
}

const (
	rlsViolation     = "row-level security"
	permissionDenied = "permission denied"
)

// wantRows reports an error unless the statement succeeded and affected n rows.
func wantRows(t *testing.T, n int64, err error, want int64, what string) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: %v", what, err)
	} else if n != want {
		t.Errorf("%s: %d rows affected, want %d", what, n, want)
	}
}

func TestRLSRole(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	var canLogin, super, bypass bool
	err := pool.QueryRow(t.Context(),
		`SELECT rolcanlogin, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = 'shelfkin_app'`,
	).Scan(&canLogin, &super, &bypass)
	if err != nil {
		t.Fatalf("looking up role shelfkin_app: %v", err)
	}
	if canLogin || super || bypass {
		t.Errorf("shelfkin_app: login %v, superuser %v, bypassrls %v; want all false", canLogin, super, bypass)
	}
}

// TestRLSGrants checks the exact privileges shelfkin_app has on every
// relation in the public schema, and that PUBLIC has none.
func TestRLSGrants(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	rows, err := pool.Query(ctx, `
		SELECT c.relname::text, string_agg(a.privilege_type, ',' ORDER BY a.privilege_type)
		FROM pg_class c
		CROSS JOIN LATERAL aclexplode(c.relacl) a
		WHERE c.relnamespace = 'public'::regnamespace
		  AND a.grantee = 'shelfkin_app'::regrole
		GROUP BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for rows.Next() {
		var rel, privs string
		if err := rows.Scan(&rel, &privs); err != nil {
			t.Fatal(err)
		}
		got[rel] = privs
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	const all = "DELETE,INSERT,SELECT,UPDATE"
	want := map[string]string{
		"categories":              "SELECT",
		"locations":               "SELECT",
		"users":                   "INSERT,SELECT,UPDATE",
		"push_tokens":             all,
		"households":              "DELETE,SELECT,UPDATE",
		"household_members":       "DELETE,SELECT",
		"household_invites":       all,
		"catalog_items":           all,
		"household_item_defaults": all,
		"pantry_entries":          all,
		"item_events":             "INSERT,SELECT",
		"expiry_notifications":    "DELETE,INSERT,SELECT",
		"effective_catalog_items": "SELECT",
		// goose_db_version and its sequence: nothing.
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("shelfkin_app privileges:\n got  %v\n want %v", got, want)
	}

	if n := count(t, pool, `
		SELECT count(*)
		FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a
		WHERE c.relnamespace = 'public'::regnamespace AND a.grantee = 0`); n != 0 {
		t.Errorf("%d privileges granted to PUBLIC on public relations, want 0", n)
	}
	if n := count(t, pool, `
		SELECT count(*) FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
		WHERE c.relnamespace = 'public'::regnamespace AND a.attacl IS NOT NULL`); n != 0 {
		t.Errorf("%d column-level grants in public, want 0", n)
	}
	var usage bool
	if err := pool.QueryRow(ctx, `SELECT has_schema_privilege('shelfkin_app', 'public', 'USAGE')`).Scan(&usage); err != nil {
		t.Fatal(err)
	}
	if !usage {
		t.Error("shelfkin_app has no USAGE on schema public")
	}
}

// TestRLSHelperFunctions checks the functions the policies call: SECURITY
// DEFINER, STABLE, a fixed empty search_path, and executable by shelfkin_app
// but not by PUBLIC.
func TestRLSHelperFunctions(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	for _, fn := range []string{"app_user_household_ids", "app_user_owned_household_ids"} {
		var (
			secdef, appExec, publicExec bool
			volatile                    string
			config                      []string
		)
		err := pool.QueryRow(t.Context(), `
			SELECT p.prosecdef, p.provolatile::text, p.proconfig,
			       has_function_privilege('shelfkin_app', p.oid, 'EXECUTE'),
			       EXISTS (SELECT 1 FROM aclexplode(p.proacl) a WHERE a.grantee = 0)
			FROM pg_proc p
			WHERE p.proname = $1 AND p.pronamespace = 'public'::regnamespace`, fn,
		).Scan(&secdef, &volatile, &config, &appExec, &publicExec)
		if err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		if !secdef || volatile != "s" || !reflect.DeepEqual(config, []string{`search_path=""`}) || !appExec || publicExec {
			t.Errorf("%s: security definer %v, volatility %q, config %q, shelfkin_app execute %v, PUBLIC execute %v; "+
				`want true, "s", ["search_path=\"\""], true, false`, fn, secdef, volatile, config, appExec, publicExec)
		}
	}
}

// TestRLSPolicies checks that row-level security is enabled on exactly the
// household tables, and that every policy applies to shelfkin_app and is
// keyed on the helper functions.
func TestRLSPolicies(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	rows, err := pool.Query(ctx, `
		SELECT relname::text FROM pg_class
		WHERE relnamespace = 'public'::regnamespace AND relkind IN ('r', 'p') AND relrowsecurity
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for tbl := range rlsTables {
		want = append(want, tbl)
	}
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tables with RLS:\n got  %v\n want %v", got, want)
	}

	rows, err = pool.Query(ctx, `
		SELECT tablename::text, policyname::text, roles::text[], coalesce(qual, '') || ' ' || coalesce(with_check, '')
		FROM pg_policies WHERE schemaname = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	type policy struct {
		Table, Name string
		Roles       []string
		Expr        string
	}
	policies, err := pgx.CollectRows(rows, pgx.RowToStructByPos[policy])
	if err != nil {
		t.Fatal(err)
	}
	perTable := map[string]int{}
	for _, p := range policies {
		perTable[p.Table]++
		if !reflect.DeepEqual(p.Roles, []string{"shelfkin_app"}) {
			t.Errorf("policy %s on %s applies to %v, want [shelfkin_app]", p.Name, p.Table, p.Roles)
		}
		if !strings.Contains(p.Expr, "app_user_household_ids()") && !strings.Contains(p.Expr, "app_user_owned_household_ids()") {
			t.Errorf("policy %s on %s does not use the household helper functions: %s", p.Name, p.Table, p.Expr)
		}
	}
	for tbl := range rlsTables {
		if perTable[tbl] == 0 {
			t.Errorf("table %s has RLS but no policies", tbl)
		}
	}
}

// TestRLSGuardEveryTableDecided fails when a table in the public schema has
// neither row-level security nor a place on the allowlist, so a new table
// cannot ship without a decision about who may see its rows.
func TestRLSGuardEveryTableDecided(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	rows, err := pool.Query(t.Context(), `
		SELECT relname::text FROM pg_class
		WHERE relnamespace = 'public'::regnamespace AND relkind IN ('r', 'p') AND NOT relrowsecurity
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	for _, tbl := range tables {
		if !noRLSAllowlist[tbl] {
			t.Errorf("table %s has no row-level security and is not on the no-RLS allowlist; "+
				"add policies for it (see docs/data-model.md, Access control) or allowlist it in this test", tbl)
		}
	}
}

// TestRLSMembersSeeOnlyTheirHousehold reads every household table and the
// effective_catalog_items view as A's owner and as a plain member of A.
func TestRLSMembersSeeOnlyTheirHousehold(t *testing.T) {
	t.Parallel()
	f := newRLSFixture(t)

	for _, user := range []struct{ name, id string }{{"ownerA", f.ownerA}, {"memberA", f.memberA}} {
		for rel, col := range householdRelations {
			// Superuser counts of A's rows; built-ins are visible to everyone.
			want := count(t, f.pool, fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`, rel, col), f.a.id)
			if want == 0 {
				t.Fatalf("fixture has no %s rows for A", rel)
			}
			if rel == "catalog_items" {
				want += f.builtIns
			}
			if rel == "household_invites" && user.id == f.memberA {
				want = 0 // invites are owner-only
			}
			if got := countAs(t, f.pool, user.id, fmt.Sprintf(`SELECT count(*) FROM %s`, rel)); got != want {
				t.Errorf("as %s: %d rows in %s, want %d", user.name, got, rel, want)
			}
			if got := countAs(t, f.pool, user.id, fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`, rel, col), f.b.id); got != 0 {
				t.Errorf("as %s: %d of B's rows visible in %s, want 0", user.name, got, rel)
			}
		}

		// B's custom item does not appear in the view under any household, and
		// A's override shows through it.
		if got := countAs(t, f.pool, user.id,
			`SELECT count(*) FROM effective_catalog_items WHERE catalog_item_id = $1`, f.b.item); got != 0 {
			t.Errorf("as %s: B's custom item appears %d times in effective_catalog_items, want 0", user.name, got)
		}
		if got := countAs(t, f.pool, user.id,
			`SELECT count(*) FROM effective_catalog_items WHERE household_id = $1`, f.a.id); got != f.builtIns+1 {
			t.Errorf("as %s: %d effective_catalog_items rows for A, want %d (built-ins plus A's custom item)",
				user.name, got, f.builtIns+1)
		}
		if got := countAs(t, f.pool, user.id, `
			SELECT count(*) FROM effective_catalog_items
			WHERE catalog_item_id = $1 AND category_overridden AND category_id = $2 AND location_id = $3`,
			f.builtIn, catProduce, locFreezer); got != 1 {
			t.Errorf("as %s: A's override visible in %d effective_catalog_items rows, want 1", user.name, got)
		}
	}
}

// TestRLSCrossHouseholdWritesBlocked tries to change B's rows as A's owner
// and as a plain member of A.
func TestRLSCrossHouseholdWritesBlocked(t *testing.T) {
	t.Parallel()
	f := newRLSFixture(t)
	b := f.b

	noRows := []struct{ what, sql string }{
		{"rename B", `UPDATE households SET name = 'Mine' WHERE id = $1`},
		{"revoke B's invites", `UPDATE household_invites SET revoked_at = now() WHERE household_id = $1`},
		{"rename B's custom item", `UPDATE catalog_items SET name = 'Mine' WHERE household_id = $1`},
		{"change B's defaults", `UPDATE household_item_defaults SET category_id = NULL WHERE household_id = $1`},
		{"change B's entries", `UPDATE pantry_entries SET level = 'low' WHERE household_id = $1`},
		{"delete B", `DELETE FROM households WHERE id = $1`},
		{"remove B's members", `DELETE FROM household_members WHERE household_id = $1`},
		{"delete B's invites", `DELETE FROM household_invites WHERE household_id = $1`},
		{"delete B's custom items", `DELETE FROM catalog_items WHERE household_id = $1`},
		{"delete B's defaults", `DELETE FROM household_item_defaults WHERE household_id = $1`},
		{"delete B's entries", `DELETE FROM pantry_entries WHERE household_id = $1`},
	}
	rejected := []struct {
		what, sql string
		args      []any
	}{
		{"insert entry into B", `
			INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level)
			VALUES (uuidv7(), $1, $2, $3, 'full')`, []any{b.id, f.builtIn2, locFridge}},
		{"insert event into B", `
			INSERT INTO item_events (id, household_id, entry_id, catalog_item_id, type, snapshot)
			VALUES (uuidv7(), $1, $2, $3, 'level_changed', '{}')`, []any{b.id, b.entry, b.item}},
		{"insert default into B", `
			INSERT INTO household_item_defaults (household_id, catalog_item_id, category_id)
			VALUES ($1, $2, $3)`, []any{b.id, f.builtIn2, catDairy}},
		{"insert custom item into B", `
			INSERT INTO catalog_items (household_id, name, default_category_id, default_location_id)
			VALUES ($1, 'Sneaky', $2, $3)`, []any{b.id, catBaking, locPantry}},
		{"insert invite into B", `
			INSERT INTO household_invites (household_id, code, expires_at)
			VALUES ($1, 'SNEAKY23', now() + interval '7 days')`, []any{b.id}},
		{"move A's entry to B", `UPDATE pantry_entries SET household_id = $1 WHERE id = $2`, []any{b.id, f.a.entry}},
		{"move A's custom item to B", `UPDATE catalog_items SET household_id = $1 WHERE id = $2`, []any{b.id, f.a.item}},
	}

	for _, user := range []struct{ name, id string }{{"ownerA", f.ownerA}, {"memberA", f.memberA}} {
		for _, c := range noRows {
			n, err := execAs(t, f.pool, user.id, c.sql, b.id)
			wantRows(t, n, err, 0, user.name+": "+c.what)
		}
		for _, c := range rejected {
			if user.id == f.memberA && strings.Contains(c.what, "invite") {
				continue // covered by TestRLSMemberIsNotOwner
			}
			_, err := execAs(t, f.pool, user.id, c.sql, c.args...)
			wantDenied(t, err, rlsViolation, user.name+": "+c.what)
		}
	}
}

func TestRLSItemEventsAreAppendOnly(t *testing.T) {
	t.Parallel()
	f := newRLSFixture(t)

	_, err := execAs(t, f.pool, f.ownerA, `UPDATE item_events SET changes = '{"x": 1}' WHERE id = $1`, f.a.event)
	wantDenied(t, err, permissionDenied, "update A's own event")
	_, err = execAs(t, f.pool, f.ownerA, `DELETE FROM item_events WHERE id = $1`, f.a.event)
	wantDenied(t, err, permissionDenied, "delete A's own event")
}

func TestRLSBuiltInCatalogIsReadOnly(t *testing.T) {
	t.Parallel()
	f := newRLSFixture(t)

	if got := countAs(t, f.pool, f.memberA, `SELECT count(*) FROM catalog_items WHERE household_id IS NULL`); got != f.builtIns {
		t.Errorf("%d built-in items visible, want %d", got, f.builtIns)
	}

	_, err := execAs(t, f.pool, f.memberA, `
		INSERT INTO catalog_items (household_id, name, default_category_id, default_location_id)
		VALUES (NULL, 'Unobtainium', $1, $2)`, catBaking, locPantry)
	wantDenied(t, err, rlsViolation, "insert built-in item")

	// The built-in row is visible but not updatable, so the UPDATE matches
	// nothing rather than failing.
	n, err := execAs(t, f.pool, f.memberA, `UPDATE catalog_items SET name = 'Renamed' WHERE id = $1`, f.builtIn)
	wantRows(t, n, err, 0, "update built-in item")
	n, err = execAs(t, f.pool, f.memberA, `DELETE FROM catalog_items WHERE id = $1`, f.builtIn)
	wantRows(t, n, err, 0, "delete built-in item")

	_, err = execAs(t, f.pool, f.memberA, `UPDATE catalog_items SET household_id = NULL WHERE id = $1`, f.a.item)
	wantDenied(t, err, rlsViolation, "turn A's custom item into a built-in")
}

// TestRLSMembersWriteTheirHousehold covers what a plain member may change:
// custom items, item defaults, entries and events.
func TestRLSMembersWriteTheirHousehold(t *testing.T) {
	t.Parallel()
	f := newRLSFixture(t)
	a := f.a.id

	steps := []struct {
		what string
		sql  string
		args []any
	}{
		{"insert custom item", `
			INSERT INTO catalog_items (id, household_id, name, default_category_id, default_location_id)
			VALUES ('019a0000-0000-7000-8000-000000000001', $1, 'Pickles', $2, $3)`, []any{a, catBaking, locPantry}},
		{"update custom item", `UPDATE catalog_items SET name = 'Gherkins' WHERE id = '019a0000-0000-7000-8000-000000000001'`, nil},
		{"delete custom item", `DELETE FROM catalog_items WHERE id = '019a0000-0000-7000-8000-000000000001'`, nil},
		{"insert default", `
			INSERT INTO household_item_defaults (household_id, catalog_item_id, location_id)
			VALUES ($1, $2, $3)`, []any{a, f.builtIn2, locFreezer}},
		{"update default", `UPDATE household_item_defaults SET location_id = $3 WHERE household_id = $1 AND catalog_item_id = $2`,
			[]any{a, f.builtIn2, locFridge}},
		{"delete default", `DELETE FROM household_item_defaults WHERE household_id = $1 AND catalog_item_id = $2`, []any{a, f.builtIn2}},
		{"insert entry", `
			INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level)
			VALUES ('019a0000-0000-7000-8000-000000000002', $1, $2, $3, 'full')`, []any{a, f.builtIn2, locFridge}},
		{"update entry", `UPDATE pantry_entries SET level = 'low' WHERE id = '019a0000-0000-7000-8000-000000000002'`, nil},
		{"delete entry", `DELETE FROM pantry_entries WHERE id = '019a0000-0000-7000-8000-000000000002'`, nil},
		{"insert event", `
			INSERT INTO item_events (id, household_id, entry_id, catalog_item_id, type, snapshot)
			VALUES ('019a0000-0000-7000-8000-000000000003', $1, $2, $3, 'entry_deleted', '{}')`,
			[]any{a, "019a0000-0000-7000-8000-000000000002", f.builtIn2}},
	}
	err := inTx(t, f.pool, f.memberA, func(tx pgx.Tx) error {
		for _, s := range steps {
			tag, err := tx.Exec(t.Context(), s.sql, s.args...)
			if err != nil {
				return fmt.Errorf("%s: %w", s.what, err)
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("%s: %d rows affected, want 1", s.what, tag.RowsAffected())
			}
		}
		// A retried event insert is ignored, not an error.
		tag, err := tx.Exec(t.Context(), `
			INSERT INTO item_events (id, household_id, entry_id, catalog_item_id, type, snapshot)
			VALUES ('019a0000-0000-7000-8000-000000000003', $1, $2, $3, 'entry_deleted', '{}')
			ON CONFLICT (id) DO NOTHING`, a, "019a0000-0000-7000-8000-000000000002", f.builtIn2)
		if err != nil {
			return fmt.Errorf("retried event: %w", err)
		}
		if tag.RowsAffected() != 0 {
			return fmt.Errorf("retried event: %d rows affected, want 0", tag.RowsAffected())
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
}

// TestRLSNoHouseholdAccess reads as a user in neither household and in a
// transaction with no user.
func TestRLSNoHouseholdAccess(t *testing.T) {
	t.Parallel()
	f := newRLSFixture(t)

	for _, user := range []struct{ name, id string }{{"outsider", f.outsider}, {"no user", ""}} {
		for rel, col := range householdRelations {
			q := fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s IS NOT NULL`, rel, col)
			if got := countAs(t, f.pool, user.id, q); got != 0 {
				t.Errorf("as %s: %d household rows in %s, want 0", user.name, got, rel)
			}
		}
		if got := countAs(t, f.pool, user.id, `SELECT count(*) FROM catalog_items WHERE household_id IS NULL`); got != f.builtIns {
			t.Errorf("as %s: %d built-in items visible, want %d", user.name, got, f.builtIns)
		}
		if got := countAs(t, f.pool, user.id, `SELECT count(*) FROM catalog_items`); got != f.builtIns {
			t.Errorf("as %s: %d catalog items visible, want only the %d built-ins", user.name, got, f.builtIns)
		}
	}
}

// TestRLSMemberIsNotOwner covers the owner-only policies, which back up the
// API's checks: a plain member cannot touch invites, rename or delete the
// household, or remove anyone but themselves; the owner can.
func TestRLSMemberIsNotOwner(t *testing.T) {
	t.Parallel()
	f := newRLSFixture(t)
	a := f.a.id
	const newInvite = `
		INSERT INTO household_invites (household_id, code, expires_at)
		VALUES ($1, 'NEWCODE2', now() + interval '7 days')`

	if got := countAs(t, f.pool, f.memberA, `SELECT count(*) FROM household_invites`); got != 0 {
		t.Errorf("member sees %d invites, want 0", got)
	}
	_, err := execAs(t, f.pool, f.memberA, newInvite, a)
	wantDenied(t, err, rlsViolation, "member creates invite")

	member := []struct{ what, sql string }{
		{"member revokes invite", `UPDATE household_invites SET revoked_at = now() WHERE household_id = $1`},
		{"member deletes invite", `DELETE FROM household_invites WHERE household_id = $1`},
		{"member renames A", `UPDATE households SET name = 'Renamed' WHERE id = $1`},
		{"member deletes A", `DELETE FROM households WHERE id = $1`},
		{"member removes the owner", `DELETE FROM household_members WHERE household_id = $1 AND user_id = '` + f.ownerA + `'`},
		{"member removes another member", `DELETE FROM household_members WHERE household_id = $1 AND user_id = '` + f.member2A + `'`},
	}
	for _, c := range member {
		n, err := execAs(t, f.pool, f.memberA, c.sql, a)
		wantRows(t, n, err, 0, c.what)
	}
	n, err := execAs(t, f.pool, f.memberA, `DELETE FROM household_members WHERE household_id = $1 AND user_id = $2`, a, f.memberA)
	wantRows(t, n, err, 1, "member leaves")

	owner := []struct{ what, sql string }{
		{"owner creates invite", newInvite},
		{"owner revokes invite", `UPDATE household_invites SET revoked_at = now() WHERE household_id = $1`},
		{"owner deletes invite", `DELETE FROM household_invites WHERE household_id = $1`},
		{"owner renames A", `UPDATE households SET name = 'Renamed' WHERE id = $1`},
		{"owner removes a member", `DELETE FROM household_members WHERE household_id = $1 AND user_id = '` + f.member2A + `'`},
		{"owner deletes A", `DELETE FROM households WHERE id = $1`},
	}
	for _, c := range owner {
		n, err := execAs(t, f.pool, f.ownerA, c.sql, a)
		wantRows(t, n, err, 1, c.what)
	}
}

// TestRLSNoDirectMembershipWrites: households and household_members are only
// written through privileged functions (#14, #15, #16), so ownership cannot
// be transferred, and nobody can add themselves to a household, with plain
// statements.
func TestRLSNoDirectMembershipWrites(t *testing.T) {
	t.Parallel()
	f := newRLSFixture(t)
	a := f.a.id

	cases := []struct {
		what, sql string
		args      []any
	}{
		{"demote the owner", `UPDATE household_members SET role = 'member' WHERE household_id = $1 AND user_id = $2`, []any{a, f.ownerA}},
		{"promote a member", `UPDATE household_members SET role = 'owner' WHERE household_id = $1 AND user_id = $2`, []any{a, f.memberA}},
		{"create a household", `INSERT INTO households (name) VALUES ('New')`, nil},
		{"add a member", `INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'member')`, []any{a, f.outsider}},
		{"join as owner", `INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{f.b.id, f.ownerA}},
	}
	for _, user := range []struct{ name, id string }{
		{"ownerA", f.ownerA}, {"memberA", f.memberA}, {"outsider", f.outsider}, {"no user", ""},
	} {
		for _, c := range cases {
			_, err := execAs(t, f.pool, user.id, c.sql, c.args...)
			wantDenied(t, err, permissionDenied, user.name+": "+c.what)
		}
	}
}

func TestRLSOwnerCannotLeave(t *testing.T) {
	t.Parallel()
	f := newRLSFixture(t)

	n, err := execAs(t, f.pool, f.ownerA, `DELETE FROM household_members WHERE household_id = $1 AND user_id = $2`, f.a.id, f.ownerA)
	wantRows(t, n, err, 0, "owner deletes their own owner row")
}

// TestRLSUserTablesScopedByAPI documents that users, push_tokens and
// expiry_notifications have no row-level security: through shelfkin_app any
// user can read every row, so every API query on them must filter by the
// caller's user ID.
func TestRLSUserTablesScopedByAPI(t *testing.T) {
	t.Parallel()
	f := newRLSFixture(t)

	for _, tbl := range []string{"users", "push_tokens", "expiry_notifications"} {
		var rls bool
		if err := f.pool.QueryRow(t.Context(), `SELECT relrowsecurity FROM pg_class WHERE oid = $1::regclass`, tbl).Scan(&rls); err != nil {
			t.Fatal(err)
		}
		if rls {
			t.Errorf("%s has row-level security, want none (scoped by the API)", tbl)
		}
	}
	checks := []struct {
		what string
		sql  string
		want int
	}{
		{"users", `SELECT count(*) FROM users`, 5},
		{"B's owner's push tokens", `SELECT count(*) FROM push_tokens WHERE user_id = '` + f.ownerB + `'`, 1},
		{"B's owner's expiry notifications", `SELECT count(*) FROM expiry_notifications WHERE user_id = '` + f.ownerB + `'`, 1},
	}
	for _, c := range checks {
		if got := countAs(t, f.pool, f.outsider, c.sql); got != c.want {
			t.Errorf("as outsider: %d rows of %s, want %d", got, c.what, c.want)
		}
	}
}

// TestRLSRoleMigrationInParallelDatabases migrates two databases on the same
// server at once; the server-wide role must not make either fail.
func TestRLSRoleMigrationInParallelDatabases(t *testing.T) {
	t.Parallel()
	for i := range 2 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			pool := dbtest.New(t)
			if n := count(t, pool, `SELECT count(*) FROM pg_roles WHERE rolname = 'shelfkin_app'`); n != 1 {
				t.Errorf("%d shelfkin_app roles, want 1", n)
			}
		})
	}
}

// roleStatement returns the migration's CREATE ROLE statement with the role
// renamed, so the concurrency test can race on a role of its own instead of
// the shared shelfkin_app.
func roleStatement(t *testing.T, role string) string {
	t.Helper()
	src, err := fs.ReadFile(db.Migrations(), rlsMigration)
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	const begin, end = "-- +goose StatementBegin", "-- +goose StatementEnd"
	i := strings.Index(s, begin)
	j := strings.Index(s, end)
	if i < 0 || j < i {
		t.Fatalf("no StatementBegin/End block in %s", rlsMigration)
	}
	stmt := s[i+len(begin) : j]
	if !strings.Contains(stmt, "CREATE ROLE shelfkin_app") {
		t.Fatalf("first statement block in %s is not the role creation:\n%s", rlsMigration, stmt)
	}
	return strings.ReplaceAll(stmt, "shelfkin_app", role)
}

// TestRLSRoleCreationToleratesConcurrentCreate runs the migration's role
// statement in two transactions that overlap: the second one blocks on the
// first one's uncommitted role and must succeed once the first commits.
func TestRLSRoleCreationToleratesConcurrentCreate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	role := "shelfkin_test_role_" + strings.ReplaceAll(newID(t, pool)[24:], "-", "")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	})
	stmt := roleStatement(t, role)

	c1, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Release()
	c2, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Release()

	tx1, err := c1.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if _, err := tx1.Exec(ctx, stmt); err != nil {
		t.Fatalf("first create: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := c2.Exec(ctx, stmt)
		done <- err
	}()

	// Wait until the second statement is waiting on the first transaction.
	pid := c2.Conn().PgConn().PID()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx,
			`SELECT coalesce(wait_event_type = 'Lock', false) FROM pg_stat_activity WHERE pid = $1`, pid,
		).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("second create finished before the first committed (err %v); want it to wait", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("second create never waited on the first")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("second create, after the first committed: %v", err)
	}
	// Once the role exists the statement is a no-op.
	if _, err := pool.Exec(ctx, stmt); err != nil {
		t.Errorf("create when the role exists: %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM pg_roles WHERE rolname = $1`, role); n != 1 {
		t.Errorf("%d roles named %s, want 1", n, role)
	}
}

// TestRLSMigrationDownUp rolls back this migration (and any later ones),
// checks the policies, helper functions and grants are gone but the
// server-wide role is kept, then migrates up again.
func TestRLSMigrationDownUp(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := db.NewProvider(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	var this, previous int64 = -1, 0
	for _, s := range provider.ListSources() {
		if filepath.Base(s.Path) == rlsMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", rlsMigration)
	}

	type state struct{ Policies, RLSTables, Functions, Grants, Role int }
	get := func() state {
		t.Helper()
		var s state
		if err := pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM pg_policies WHERE schemaname = 'public'),
			       (SELECT count(*) FROM pg_class WHERE relnamespace = 'public'::regnamespace AND relrowsecurity),
			       (SELECT count(*) FROM pg_proc WHERE pronamespace = 'public'::regnamespace
			                                     AND proname IN ('app_user_household_ids', 'app_user_owned_household_ids')),
			       (SELECT count(*) FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a
			         WHERE c.relnamespace = 'public'::regnamespace AND a.grantee = 'shelfkin_app'::regrole)
			     + (SELECT count(*) FROM pg_namespace n CROSS JOIN LATERAL aclexplode(n.nspacl) a
			         WHERE n.nspname = 'public' AND a.grantee = 'shelfkin_app'::regrole),
			       (SELECT count(*) FROM pg_roles WHERE rolname = 'shelfkin_app')`,
		).Scan(&s.Policies, &s.RLSTables, &s.Functions, &s.Grants, &s.Role); err != nil {
			t.Fatal(err)
		}
		return s
	}

	up := get()
	if up.Policies == 0 || up.RLSTables != len(rlsTables) || up.Functions != 2 || up.Grants == 0 || up.Role != 1 {
		t.Fatalf("before down: %+v", up)
	}
	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	if got, want := get(), (state{Role: 1}); got != want {
		t.Fatalf("after down: %+v; want %+v (only the role kept)", got, want)
	}
	if _, err := provider.UpTo(ctx, this); err != nil {
		t.Fatalf("up to %d: %v", this, err)
	}
	if got := get(); got != up {
		t.Fatalf("after up: %+v; want %+v", got, up)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
}
