package db_test

import (
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
)

// catalogRefsMigration tightens the WITH CHECK of pantry_entries and
// household_item_defaults so they can only point at catalog items the row's
// household may use (#73).
const catalogRefsMigration = "20261103020000_rls_catalog_refs.sql"

// missingItem is a catalog item ID that exists nowhere.
const missingItem = "019a0000-0000-7000-8000-0000000000ff"

// catalogRefsFixture is the RLS fixture plus a third household C whose owner,
// memberAC, is also a plain member of A. C has its own custom item.
type catalogRefsFixture struct {
	*rlsFixture
	memberAC string
	c        rlsHousehold
}

func newCatalogRefsFixture(t *testing.T) *catalogRefsFixture {
	t.Helper()
	f := &catalogRefsFixture{rlsFixture: newRLSFixture(t)}
	f.memberAC = insertUser(t, f.pool, "fb-member-ac")
	f.c.id = insertHousehold(t, f.pool, "C", f.memberAC)
	mustInsertMember(t, f.pool, f.c.id, f.memberAC, "owner")
	mustInsertMember(t, f.pool, f.a.id, f.memberAC, "member")
	f.c.item = mustAddItem(t, f.pool, f.c.id, "C chutney", catBaking, locPantry, nil)
	return f
}

const insertEntrySQL = `
	INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level)
	VALUES (uuidv7(), $1, $2, $3, 'full')`

// TestRLSEntryCatalogItem checks that an entry can only point at a built-in
// item or a custom item of its own household, and that "another household's
// item" and "no such item" fail the same way.
func TestRLSEntryCatalogItem(t *testing.T) {
	t.Parallel()
	f := newCatalogRefsFixture(t)
	a := f.a.id

	denied := []struct {
		what, user, sql string
		args            []any
	}{
		{"entry in A with B's custom item", f.memberA, insertEntrySQL, []any{a, f.b.item, locFridge}},
		{"entry in A with a missing item", f.memberA, insertEntrySQL, []any{a, missingItem, locFridge}},
		{"entry in A with C's custom item, as a member of A and C", f.memberAC, insertEntrySQL, []any{a, f.c.item, locFridge}},
		{"move A's entry to B's custom item", f.memberA,
			`UPDATE pantry_entries SET catalog_item_id = $2 WHERE id = $1`, []any{f.a.entry, f.b.item}},
		{"move A's entry to a missing item", f.memberA,
			`UPDATE pantry_entries SET catalog_item_id = $2 WHERE id = $1`, []any{f.a.entry, missingItem}},
		{"move A's entry to C's custom item, as a member of A and C", f.memberAC,
			`UPDATE pantry_entries SET catalog_item_id = $2 WHERE id = $1`, []any{f.a.entry, f.c.item}},
		{"upsert A's entry onto B's custom item", f.memberA, `
			INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level)
			VALUES ($1, $2, $3, $4, 'full')
			ON CONFLICT (id) DO UPDATE SET catalog_item_id = EXCLUDED.catalog_item_id`,
			[]any{f.a.entry, a, f.b.item, locFridge}},
	}
	for _, d := range denied {
		_, err := execAs(t, f.pool, d.user, d.sql, d.args...)
		wantDenied(t, err, rlsViolation, d.what)
	}

	allowed := []struct {
		what, user, sql string
		args            []any
	}{
		{"entry in A with a built-in item", f.memberA, insertEntrySQL, []any{a, f.builtIn2, locFridge}},
		{"entry in A with A's custom item", f.memberA, insertEntrySQL, []any{a, f.a.item, locFridge}},
		{"entry in A with A's custom item, as a member of A and C", f.memberAC, insertEntrySQL, []any{a, f.a.item, locFridge}},
		{"entry in C with C's custom item", f.memberAC, insertEntrySQL, []any{f.c.id, f.c.item, locFridge}},
		{"move A's entry to a built-in item", f.memberA,
			`UPDATE pantry_entries SET catalog_item_id = $2 WHERE id = $1`, []any{f.a.entry, f.builtIn}},
		{"change another column of A's entry", f.memberA,
			`UPDATE pantry_entries SET level = 'low' WHERE id = $1`, []any{f.a.entry}},
	}
	for _, s := range allowed {
		n, err := execAs(t, f.pool, s.user, s.sql, s.args...)
		wantRows(t, n, err, 1, s.what)
	}
}

const upsertDefaultSQL = `
	INSERT INTO household_item_defaults (household_id, catalog_item_id, location_id)
	VALUES ($1, $2, $3)
	ON CONFLICT (household_id, catalog_item_id) DO UPDATE SET location_id = EXCLUDED.location_id`

// TestRLSDefaultCatalogItem checks that item defaults can only be set for
// built-in items.
func TestRLSDefaultCatalogItem(t *testing.T) {
	t.Parallel()
	f := newCatalogRefsFixture(t)
	a := f.a.id
	const insertDefault = `
		INSERT INTO household_item_defaults (household_id, catalog_item_id, location_id)
		VALUES ($1, $2, $3)`

	denied := []struct {
		what, user, sql string
		args            []any
	}{
		{"default (A, B's custom item)", f.memberA, insertDefault, []any{a, f.b.item, locFreezer}},
		{"default (A, A's custom item)", f.memberA, insertDefault, []any{a, f.a.item, locFreezer}},
		{"default (A, missing item)", f.memberA, insertDefault, []any{a, missingItem, locFreezer}},
		{"default (A, C's custom item), as a member of A and C", f.memberAC, insertDefault, []any{a, f.c.item, locFreezer}},
		{"default (C, C's custom item)", f.memberAC, insertDefault, []any{f.c.id, f.c.item, locFreezer}},
		{"upsert default (A, B's custom item)", f.memberA, upsertDefaultSQL, []any{a, f.b.item, locFreezer}},
		{"upsert default (A, A's custom item)", f.memberA, upsertDefaultSQL, []any{a, f.a.item, locFreezer}},
		{"move A's default to A's custom item", f.memberA,
			`UPDATE household_item_defaults SET catalog_item_id = $2 WHERE household_id = $1 AND catalog_item_id = $3`,
			[]any{a, f.a.item, f.builtIn}},
		{"move A's default to B's custom item", f.memberA,
			`UPDATE household_item_defaults SET catalog_item_id = $2 WHERE household_id = $1 AND catalog_item_id = $3`,
			[]any{a, f.b.item, f.builtIn}},
	}
	for _, d := range denied {
		_, err := execAs(t, f.pool, d.user, d.sql, d.args...)
		wantDenied(t, err, rlsViolation, d.what)
	}

	allowed := []struct {
		what string
		sql  string
		args []any
	}{
		{"default (A, built-in item)", insertDefault, []any{a, f.builtIn2, locFreezer}},
		// builtIn2 has no default in A yet: the insert path of the upsert.
		{"upsert new default (A, built-in item)", upsertDefaultSQL, []any{a, f.builtIn2, locFreezer}},
		// builtIn already has a default in A: the update path of the upsert.
		{"upsert existing default (A, built-in item)", upsertDefaultSQL, []any{a, f.builtIn, locFridge}},
		{"update A's default", `UPDATE household_item_defaults SET category_id = $3 WHERE household_id = $1 AND catalog_item_id = $2`,
			[]any{a, f.builtIn, catBaking}},
	}
	for _, s := range allowed {
		n, err := execAs(t, f.pool, f.memberA, s.sql, s.args...)
		wantRows(t, n, err, 1, s.what)
	}
}

// TestRLSCatalogRefsDoNotBlockOwner tries to pin B's custom item from A, in
// transactions that would commit, then checks B's owner can still delete the
// item and the household.
func TestRLSCatalogRefsDoNotBlockOwner(t *testing.T) {
	t.Parallel()
	f := newCatalogRefsFixture(t)
	ctx := t.Context()

	attempts := []struct {
		what, sql string
		args      []any
	}{
		{"entry in A with B's custom item", insertEntrySQL, []any{f.a.id, f.b.item, locFridge}},
		{"move A's entry to B's custom item", `UPDATE pantry_entries SET catalog_item_id = $2 WHERE id = $1`,
			[]any{f.a.entry, f.b.item}},
		{"default (A, B's custom item)", upsertDefaultSQL, []any{f.a.id, f.b.item, locFreezer}},
	}
	for _, at := range attempts {
		err := dbtx.WithUser(ctx, f.pool, pgUUID(t, f.memberA), func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, at.sql, at.args...)
			return err
		})
		wantDenied(t, err, rlsViolation, at.what)
	}
	if n := countRows(t, f, `SELECT count(*) FROM pantry_entries WHERE catalog_item_id = $1 AND household_id <> $2`,
		f.b.item, f.b.id); n != 0 {
		t.Fatalf("%d entries outside B point at B's custom item, want 0", n)
	}

	// B's own entry uses the item, so B removes it first.
	err := dbtx.WithUser(ctx, f.pool, pgUUID(t, f.ownerB), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM pantry_entries WHERE catalog_item_id = $1`, f.b.item); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM catalog_items WHERE id = $1`, f.b.item)
		if err == nil && tag.RowsAffected() != 1 {
			t.Errorf("delete B's custom item: %d rows affected, want 1", tag.RowsAffected())
		}
		return err
	})
	if err != nil {
		t.Fatalf("B's owner deleting B's custom item: %v", err)
	}

	err = dbtx.WithUser(ctx, f.pool, pgUUID(t, f.ownerB), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM households WHERE id = $1`, f.b.id)
		if err == nil && tag.RowsAffected() != 1 {
			t.Errorf("delete household B: %d rows affected, want 1", tag.RowsAffected())
		}
		return err
	})
	if err != nil {
		t.Fatalf("B's owner deleting household B: %v", err)
	}
}

// countRows runs a count(*) query as the superuser, bypassing RLS.
func countRows(t *testing.T, f *catalogRefsFixture, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRLSCatalogRefsMigrationDownUp rolls back this migration (and any later
// ones), checks the two policies are back to the #10 form with their USING
// clauses untouched, then migrates up again.
func TestRLSCatalogRefsMigrationDownUp(t *testing.T) {
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
		if filepath.Base(s.Path) == catalogRefsMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", catalogRefsMigration)
	}

	type policy struct{ Name, Cmd, Using, Check string }
	// The two policies this migration changes, plus item_events_insert,
	// whose WITH CHECK is the plain #10 household check.
	get := func() map[string]policy {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT policyname::text, cmd, coalesce(qual, ''), coalesce(with_check, '')
			FROM pg_policies
			WHERE schemaname = 'public'
			  AND policyname IN ('pantry_entries_member', 'household_item_defaults_member', 'item_events_insert')`)
		if err != nil {
			t.Fatal(err)
		}
		ps, err := pgx.CollectRows(rows, pgx.RowToStructByPos[policy])
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]policy{}
		for _, p := range ps {
			m[p.Name] = p
		}
		if len(m) != 3 {
			t.Fatalf("policies: %+v", m)
		}
		return m
	}

	up := get()
	plain := up["item_events_insert"].Check
	for _, name := range []string{"pantry_entries_member", "household_item_defaults_member"} {
		p := up[name]
		if p.Cmd != "ALL" || p.Using != plain {
			t.Errorf("%s after up: %+v; want FOR ALL with USING %s", name, p, plain)
		}
		if p.Check == plain {
			t.Errorf("%s after up: WITH CHECK is still the #10 check %s", name, p.Check)
		}
	}

	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	for _, name := range []string{"pantry_entries_member", "household_item_defaults_member"} {
		got, want := get()[name], policy{Name: name, Cmd: "ALL", Using: plain, Check: plain}
		if got != want {
			t.Errorf("%s after down:\n got  %+v\n want %+v", name, got, want)
		}
	}

	if _, err := provider.UpTo(ctx, this); err != nil {
		t.Fatalf("up to %d: %v", this, err)
	}
	after := get()
	for name, p := range up {
		if after[name] != p {
			t.Errorf("%s after up again:\n got  %+v\n want %+v", name, after[name], p)
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
}
