package db_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
)

// lookupRow is one row of categories or locations.
type lookupRow struct {
	ID        int16
	Key       string
	Name      string
	SortOrder int16
}

type keyName struct{ Key, Name string }

// The expected keys and names, in sort order (issue #6). Keys are permanent.
var wantCategories = []keyName{
	{"produce", "Produce"},
	{"dairy", "Dairy"},
	{"meat_seafood", "Meat & seafood"},
	{"grains", "Grains"},
	{"baking", "Baking"},
	{"spices", "Spices"},
	{"canned", "Canned goods"},
	{"condiments", "Condiments & sauces"},
	{"oils", "Oils & vinegars"},
	{"snacks", "Snacks"},
	{"beverages", "Beverages"},
	{"other", "Other"},
}

var wantLocations = []keyName{
	{"pantry", "Pantry"},
	{"fridge", "Fridge"},
	{"freezer", "Freezer"},
	{"spice_rack", "Spice rack"},
	{"counter", "Counter"},
}

var lookupTables = []struct {
	Name string
	Want []keyName
}{
	{"categories", wantCategories},
	{"locations", wantLocations},
}

var snakeKey = regexp.MustCompile(`^[a-z][a-z_]*$`)

// lookupMigrationSuffix identifies the migration that creates both tables.
const lookupMigrationSuffix = "_categories_locations.sql"

func queryLookup(ctx context.Context, t *testing.T, pool *pgxpool.Pool, table string) []lookupRow {
	t.Helper()
	rows, err := pool.Query(ctx, "SELECT id, key, name, sort_order FROM "+pgx.Identifier{table}.Sanitize()+" ORDER BY sort_order")
	if err != nil {
		t.Fatalf("querying %s: %v", table, err)
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (lookupRow, error) {
		var l lookupRow
		err := r.Scan(&l.ID, &l.Key, &l.Name, &l.SortOrder)
		return l, err
	})
	if err != nil {
		t.Fatalf("reading %s: %v", table, err)
	}
	return got
}

func TestLookupTableSchema(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	type column struct {
		Name, Type, Nullable string
		Default              string // "" when there is none
		Identity             string
	}
	want := []column{
		{"id", "smallint", "NO", "", "NO"},
		{"key", "text", "NO", "", "NO"},
		{"name", "text", "NO", "", "NO"},
		{"sort_order", "smallint", "NO", "", "NO"},
	}

	for _, tbl := range lookupTables {
		t.Run(tbl.Name, func(t *testing.T) {
			rows, err := pool.Query(ctx, `
				SELECT column_name, data_type, is_nullable, coalesce(column_default, ''), is_identity
				FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1
				ORDER BY ordinal_position`, tbl.Name)
			if err != nil {
				t.Fatal(err)
			}
			got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (column, error) {
				var c column
				err := r.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &c.Identity)
				return c, err
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("columns = %+v, want %+v", got, want)
			}

			// Exactly a primary key on id and a unique constraint on key.
			rows, err = pool.Query(ctx, `
				SELECT c.contype::text, string_agg(a.attname, ',' ORDER BY a.attnum)
				FROM pg_constraint c
				JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
				WHERE c.conrelid = $1::regclass AND c.contype IN ('p', 'u')
				GROUP BY c.oid, c.contype`, tbl.Name)
			if err != nil {
				t.Fatal(err)
			}
			constraints := map[string]string{}
			for rows.Next() {
				var typ, cols string
				if err := rows.Scan(&typ, &cols); err != nil {
					t.Fatal(err)
				}
				constraints[cols] = typ
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			wantCons := map[string]string{"id": "p", "key": "u"}
			if !reflect.DeepEqual(constraints, wantCons) {
				t.Errorf("constraints (columns -> type) = %v, want %v", constraints, wantCons)
			}

			// No sequence depends on the table (rules out serial and identity).
			var seqs int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM pg_depend d
				JOIN pg_class s ON s.oid = d.objid AND s.relkind = 'S'
				WHERE d.refobjid = $1::regclass`, tbl.Name).Scan(&seqs); err != nil {
				t.Fatal(err)
			}
			if seqs != 0 {
				t.Errorf("%s has %d sequence(s), want none", tbl.Name, seqs)
			}
		})
	}
}

func TestLookupSeedData(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	for _, tbl := range lookupTables {
		t.Run(tbl.Name, func(t *testing.T) {
			got := queryLookup(ctx, t, pool, tbl.Name)

			gotKeys := []string{}
			for _, r := range got {
				gotKeys = append(gotKeys, r.Key)
			}
			wantKeys := []string{}
			for _, w := range tbl.Want {
				wantKeys = append(wantKeys, w.Key)
			}
			// Same keys, nothing missing or extra, in the same order.
			if !reflect.DeepEqual(gotKeys, wantKeys) {
				t.Fatalf("keys ordered by sort_order = %v, want %v", gotKeys, wantKeys)
			}

			sortOrders := map[int16]string{}
			ids := map[int16]string{}
			for i, r := range got {
				if r.Name != tbl.Want[i].Name {
					t.Errorf("%s: name = %q, want %q", r.Key, r.Name, tbl.Want[i].Name)
				}
				if !snakeKey.MatchString(r.Key) {
					t.Errorf("key %q does not match %s", r.Key, snakeKey)
				}
				if prev, dup := sortOrders[r.SortOrder]; dup {
					t.Errorf("sort_order %d used by both %s and %s", r.SortOrder, prev, r.Key)
				}
				sortOrders[r.SortOrder] = r.Key
				if prev, dup := ids[r.ID]; dup {
					t.Errorf("id %d used by both %s and %s", r.ID, prev, r.Key)
				}
				ids[r.ID] = r.Key
			}
		})
	}

	// "other" sorts strictly after every other category.
	var after int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM categories
		WHERE key <> 'other'
		  AND sort_order >= (SELECT sort_order FROM categories WHERE key = 'other')`,
	).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 0 {
		t.Errorf("%d categories sort at or after other, want 0", after)
	}
}

func TestLookupConstraints(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	for _, tbl := range lookupTables {
		t.Run(tbl.Name, func(t *testing.T) {
			insert := "INSERT INTO " + pgx.Identifier{tbl.Name}.Sanitize() + " (id, key, name, sort_order) VALUES ($1, $2, $3, $4)"
			cases := []struct {
				name string
				args []any
				code string
			}{
				{"duplicate key", []any{100, tbl.Want[0].Key, "Duplicate", 100}, "23505"},
				{"null key", []any{101, nil, "No key", 101}, "23502"},
				{"null name", []any{102, "no_name", nil, 102}, "23502"},
			}
			for _, c := range cases {
				_, err := pool.Exec(ctx, insert, c.args...)
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != c.code {
					t.Errorf("%s: err = %v, want SQLSTATE %s", c.name, err, c.code)
				}
			}
		})
	}
}

// TestLookupMigrationDownUp rolls back the categories/locations migration (and
// any later ones), checks both tables are gone, migrates up again and checks
// the seed rows come back with the same IDs.
func TestLookupMigrationDownUp(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	before := map[string][]lookupRow{}
	for _, tbl := range lookupTables {
		before[tbl.Name] = queryLookup(ctx, t, pool, tbl.Name)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := db.NewProvider(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	var previous int64
	for _, s := range provider.ListSources() {
		if strings.HasSuffix(s.Path, lookupMigrationSuffix) {
			found = true
			break
		}
		previous = s.Version
	}
	if !found {
		t.Fatalf("no migration ending in %s", lookupMigrationSuffix)
	}

	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	for _, tbl := range lookupTables {
		var reg *string
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1)::text", "public."+tbl.Name).Scan(&reg); err != nil {
			t.Fatal(err)
		}
		if reg != nil {
			t.Errorf("table %s still exists after down", tbl.Name)
		}
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	for _, tbl := range lookupTables {
		after := queryLookup(ctx, t, pool, tbl.Name)
		if !reflect.DeepEqual(after, before[tbl.Name]) {
			t.Errorf("%s after down/up = %+v, want %+v", tbl.Name, after, before[tbl.Name])
		}
	}
}
