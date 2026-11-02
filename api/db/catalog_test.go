package db_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
)

// catalogMigration is the file that creates catalog_items,
// household_item_defaults, effective_catalog_items and the pg_trgm extension.
const catalogMigration = "20261102120000_catalog_items.sql"

// Seeded lookup IDs (issue #6).
const (
	catProduce   = 1
	catDairy     = 2
	catBaking    = 5
	locPantry    = 1
	locFridge    = 2
	locFreezer   = 3
	locSpiceRack = 4
)

func addItem(pool *pgxpool.Pool, t *testing.T, householdID, name any, category, location int, shelfLife any) (string, error) {
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO catalog_items (household_id, name, default_category_id, default_location_id, shelf_life_days)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		householdID, name, category, location, shelfLife).Scan(&id)
	return id, err
}

func mustAddItem(t *testing.T, pool *pgxpool.Pool, householdID any, name string, category, location int, shelfLife any) string {
	t.Helper()
	id, err := addItem(pool, t, householdID, name, category, location, shelfLife)
	if err != nil {
		t.Fatalf("inserting catalog item %q: %v", name, err)
	}
	return id
}

func setOverride(t *testing.T, pool *pgxpool.Pool, householdID, itemID string, category, location any) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO household_item_defaults (household_id, catalog_item_id, category_id, location_id)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (household_id, catalog_item_id)
		 DO UPDATE SET category_id = EXCLUDED.category_id, location_id = EXCLUDED.location_id`,
		householdID, itemID, category, location)
	if err != nil {
		t.Fatalf("setting override for %s in %s: %v", itemID, householdID, err)
	}
}

type effectiveItem struct {
	HouseholdID        string
	CatalogItemID      string
	Name               string
	IsCustom           bool
	CategoryID         int16
	LocationID         int16
	ShelfLifeDays      *int32
	CategoryOverridden bool
	LocationOverridden bool
}

// effective returns the view's row for one household and item, or nil.
func effective(t *testing.T, pool *pgxpool.Pool, householdID, itemID string) *effectiveItem {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT household_id::text, catalog_item_id::text, name, is_custom, category_id, location_id,
		       shelf_life_days, category_overridden, location_overridden
		FROM effective_catalog_items
		WHERE household_id = $1 AND catalog_item_id = $2`, householdID, itemID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (effectiveItem, error) {
		var e effectiveItem
		err := r.Scan(&e.HouseholdID, &e.CatalogItemID, &e.Name, &e.IsCustom, &e.CategoryID, &e.LocationID,
			&e.ShelfLifeDays, &e.CategoryOverridden, &e.LocationOverridden)
		return e, err
	})
	if err != nil {
		t.Fatal(err)
	}
	switch len(got) {
	case 0:
		return nil
	case 1:
		return &got[0]
	default:
		t.Fatalf("%d view rows for household %s item %s, want at most 1", len(got), householdID, itemID)
		return nil
	}
}

func int32p(v int32) *int32 { return &v }

func TestCatalogTablesColumns(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	tests := map[string][]column{
		"catalog_items": {
			{"id", "uuid", false, "uuidv7()"},
			{"household_id", "uuid", true, ""},
			{"name", "text", false, ""},
			{"default_category_id", "smallint", false, ""},
			{"default_location_id", "smallint", false, ""},
			{"shelf_life_days", "integer", true, ""},
			{"created_by", "uuid", true, ""},
			{"created_at", "timestamp with time zone", false, "now()"},
			{"updated_at", "timestamp with time zone", false, "now()"},
		},
		"household_item_defaults": {
			{"household_id", "uuid", false, ""},
			{"catalog_item_id", "uuid", false, ""},
			{"category_id", "smallint", true, ""},
			{"location_id", "smallint", true, ""},
			{"updated_by", "uuid", true, ""},
			{"updated_at", "timestamp with time zone", false, "now()"},
		},
		"effective_catalog_items": {
			{"household_id", "uuid", true, ""},
			{"catalog_item_id", "uuid", true, ""},
			{"name", "text", true, ""},
			{"is_custom", "boolean", true, ""},
			{"category_id", "smallint", true, ""},
			{"location_id", "smallint", true, ""},
			{"shelf_life_days", "integer", true, ""},
			{"category_overridden", "boolean", true, ""},
			{"location_overridden", "boolean", true, ""},
		},
	}
	for table, want := range tests {
		if got := tableColumns(t, pool, table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s columns:\n got  %+v\n want %+v", table, got, want)
		}
	}
}

func TestCatalogTablesConstraints(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	rows, err := pool.Query(t.Context(), `
		SELECT conrelid::regclass::text, pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid IN ('catalog_items'::regclass, 'household_item_defaults'::regclass)
		  AND contype <> 'n' -- NOT NULL is checked by the columns test
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	type con struct{ Table, Def string }
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (con, error) {
		var c con
		err := r.Scan(&c.Table, &c.Def)
		return c, err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []con{
		{"catalog_items", "FOREIGN KEY (created_by) REFERENCES users(id)"},
		{"catalog_items", "FOREIGN KEY (default_category_id) REFERENCES categories(id)"},
		{"catalog_items", "FOREIGN KEY (default_location_id) REFERENCES locations(id)"},
		{"catalog_items", "FOREIGN KEY (household_id) REFERENCES households(id) ON DELETE CASCADE"},
		{"catalog_items", "PRIMARY KEY (id)"},
		{"household_item_defaults", "FOREIGN KEY (catalog_item_id) REFERENCES catalog_items(id) ON DELETE CASCADE"},
		{"household_item_defaults", "FOREIGN KEY (category_id) REFERENCES categories(id)"},
		{"household_item_defaults", "FOREIGN KEY (household_id) REFERENCES households(id) ON DELETE CASCADE"},
		{"household_item_defaults", "FOREIGN KEY (location_id) REFERENCES locations(id)"},
		{"household_item_defaults", "FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL"},
		{"household_item_defaults", "PRIMARY KEY (household_id, catalog_item_id)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("constraints:\n got  %+v\n want %+v", got, want)
	}
}

func TestCatalogIndexesAndExtension(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	rows, err := pool.Query(ctx, `
		SELECT pg_get_indexdef(indexrelid)
		FROM pg_index
		WHERE indrelid = 'catalog_items'::regclass AND NOT indisprimary
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CREATE INDEX catalog_items_lower_name_trgm ON public.catalog_items USING gin (lower(name) gin_trgm_ops)",
		"CREATE UNIQUE INDEX catalog_items_household_id_lower_name_key ON public.catalog_items USING btree (household_id, lower(name)) NULLS NOT DISTINCT",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("catalog_items indexes:\n got  %q\n want %q", got, want)
	}

	if n := count(t, pool, `SELECT count(*) FROM pg_index WHERE indrelid = 'household_item_defaults'::regclass AND NOT indisprimary`); n != 0 {
		t.Errorf("household_item_defaults has %d non-primary-key indexes, want 0", n)
	}

	if n := count(t, pool, `SELECT count(*) FROM pg_extension WHERE extname = 'pg_trgm'`); n != 1 {
		t.Errorf("pg_trgm installed %d times, want 1", n)
	}

	var opts []string
	if err := pool.QueryRow(ctx, `SELECT reloptions FROM pg_class WHERE oid = 'effective_catalog_items'::regclass AND relkind = 'v'`).Scan(&opts); err != nil {
		t.Fatalf("reading view options: %v", err)
	}
	if !reflect.DeepEqual(opts, []string{"security_invoker=true"}) {
		t.Errorf("effective_catalog_items options = %q, want [security_invoker=true]", opts)
	}
}

func TestCatalogItemDefaults(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	id := mustAddItem(t, pool, nil, "Flour", catBaking, locPantry, nil)
	var version int
	var createdBy, shelfLife *string
	var timesSet bool
	if err := pool.QueryRow(ctx, `
		SELECT uuid_extract_version(id), created_by::text, shelf_life_days::text,
		       created_at IS NOT NULL AND updated_at IS NOT NULL
		FROM catalog_items WHERE id = $1`, id).Scan(&version, &createdBy, &shelfLife, &timesSet); err != nil {
		t.Fatal(err)
	}
	if version != 7 {
		t.Errorf("id version = %d, want 7", version)
	}
	if createdBy != nil || shelfLife != nil {
		t.Errorf("created_by = %v, shelf_life_days = %v, want both NULL", createdBy, shelfLife)
	}
	if !timesSet {
		t.Error("created_at/updated_at not set by default")
	}

	home := insertHousehold(t, pool, "Home", nil)
	setOverride(t, pool, home, id, nil, nil)
	var updatedSet bool
	if err := pool.QueryRow(ctx, `SELECT updated_at IS NOT NULL FROM household_item_defaults WHERE catalog_item_id = $1`, id).Scan(&updatedSet); err != nil {
		t.Fatal(err)
	}
	if !updatedSet {
		t.Error("household_item_defaults.updated_at not set by default")
	}
}

func TestCatalogBuiltInNamesUniqueIgnoringCase(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)
	_, err := addItem(pool, t, nil, "milk", catDairy, locFridge, 7)
	wantSQLState(t, err, sqlStateUnique, "second built-in milk")
}

func TestCatalogCustomNamesUniqueIgnoringCasePerHousehold(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)

	mustAddItem(t, pool, home, "Oat milk", catDairy, locFridge, nil)
	_, err := addItem(pool, t, home, "OAT MILK", catDairy, locFridge, nil)
	wantSQLState(t, err, sqlStateUnique, "second custom oat milk in one household")
}

func TestCatalogCustomNameInTwoHouseholds(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)
	cabin := insertHousehold(t, pool, "Cabin", nil)

	mustAddItem(t, pool, home, "Oat milk", catDairy, locFridge, nil)
	mustAddItem(t, pool, cabin, "Oat milk", catDairy, locFridge, nil)
}

// The index allows a custom item with a built-in item's name; the API rejects
// it (#18).
func TestCatalogCustomNameMatchingBuiltInAllowedByDatabase(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)

	mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)
	mustAddItem(t, pool, home, "milk", catDairy, locFridge, 7)
}

func TestCatalogUnknownReferences(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	_, err := addItem(pool, t, nil, "Mystery", 999, locPantry, nil)
	wantSQLState(t, err, sqlStateForeignKey, "unknown category")
	_, err = addItem(pool, t, nil, "Mystery", catBaking, 999, nil)
	wantSQLState(t, err, sqlStateForeignKey, "unknown location")
	_, err = addItem(pool, t, "00000000-0000-7000-8000-000000000000", "Mystery", catBaking, locPantry, nil)
	wantSQLState(t, err, sqlStateForeignKey, "unknown household")

	home := insertHousehold(t, pool, "Home", nil)
	flour := mustAddItem(t, pool, nil, "Flour", catBaking, locPantry, nil)
	insert := `INSERT INTO household_item_defaults (household_id, catalog_item_id, category_id, location_id) VALUES ($1, $2, $3, $4)`
	_, err = pool.Exec(ctx, insert, home, flour, 999, nil)
	wantSQLState(t, err, sqlStateForeignKey, "override with unknown category")
	_, err = pool.Exec(ctx, insert, home, flour, nil, 999)
	wantSQLState(t, err, sqlStateForeignKey, "override with unknown location")
	_, err = pool.Exec(ctx, insert, home, "00000000-0000-7000-8000-000000000000", nil, nil)
	wantSQLState(t, err, sqlStateForeignKey, "override for unknown item")
}

func TestCatalogRequiredColumns(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	_, err := addItem(pool, t, nil, nil, catBaking, locPantry, nil)
	wantSQLState(t, err, sqlStateNotNull, "null name")
	_, err = pool.Exec(t.Context(), `INSERT INTO catalog_items (name, default_location_id) VALUES ('Salt', 1)`)
	wantSQLState(t, err, sqlStateNotNull, "missing category")
	_, err = pool.Exec(t.Context(), `INSERT INTO catalog_items (name, default_category_id) VALUES ('Salt', 1)`)
	wantSQLState(t, err, sqlStateNotNull, "missing location")
}

func TestDeletingOverrideUserSetsNull(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	home := insertHousehold(t, pool, "Home", nil)
	flour := mustAddItem(t, pool, nil, "Flour", catBaking, locPantry, nil)
	if _, err := pool.Exec(ctx,
		`INSERT INTO household_item_defaults (household_id, catalog_item_id, location_id, updated_by) VALUES ($1, $2, $3, $4)`,
		home, flour, locFreezer, sam); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, sam); err != nil {
		t.Fatalf("deleting user: %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM household_item_defaults WHERE updated_by IS NULL AND location_id = $1`, locFreezer); n != 1 {
		t.Errorf("%d override rows with updated_by NULL, want 1", n)
	}
}

func TestEffectiveCatalogFallback(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	a := insertHousehold(t, pool, "A", nil)
	b := insertHousehold(t, pool, "B", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)

	want := func(household string, cat, loc int16, catOver, locOver bool) effectiveItem {
		return effectiveItem{household, milk, "Milk", false, cat, loc, int32p(7), catOver, locOver}
	}
	check := func(step string, household string, w effectiveItem) {
		t.Helper()
		got := effective(t, pool, household, milk)
		if got == nil {
			t.Fatalf("%s: no view row for household %s", step, household)
		}
		if !reflect.DeepEqual(*got, w) {
			t.Errorf("%s:\n got  %+v\n want %+v", step, *got, w)
		}
	}

	check("no override, A", a, want(a, catDairy, locFridge, false, false))
	check("no override, B", b, want(b, catDairy, locFridge, false, false))

	setOverride(t, pool, a, milk, catProduce, nil)
	check("A overrides category, A", a, want(a, catProduce, locFridge, true, false))
	check("A overrides category, B", b, want(b, catDairy, locFridge, false, false))

	setOverride(t, pool, a, milk, catProduce, locFreezer)
	check("A overrides both, A", a, want(a, catProduce, locFreezer, true, true))
	check("A overrides both, B", b, want(b, catDairy, locFridge, false, false))

	setOverride(t, pool, a, milk, nil, nil)
	check("A override row all NULL, A", a, want(a, catDairy, locFridge, false, false))
	check("A override row all NULL, B", b, want(b, catDairy, locFridge, false, false))
}

func TestEffectiveCatalogCustomItems(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	a := insertHousehold(t, pool, "A", nil)
	b := insertHousehold(t, pool, "B", nil)
	mustAddItem(t, pool, nil, "Salt", catBaking, locSpiceRack, nil)
	oat := mustAddItem(t, pool, a, "Oat milk", catDairy, locFridge, 10)

	got := effective(t, pool, a, oat)
	want := effectiveItem{a, oat, "Oat milk", true, catDairy, locFridge, int32p(10), false, false}
	if got == nil || !reflect.DeepEqual(*got, want) {
		t.Errorf("custom item for A:\n got  %+v\n want %+v", got, want)
	}
	if got := effective(t, pool, b, oat); got != nil {
		t.Errorf("A's custom item visible to B: %+v", *got)
	}

	// Each household sees every built-in plus only its own custom items.
	if n := count(t, pool, `SELECT count(*) FROM effective_catalog_items WHERE household_id = $1`, a); n != 2 {
		t.Errorf("A sees %d items, want 2", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM effective_catalog_items WHERE household_id = $1`, b); n != 1 {
		t.Errorf("B sees %d items, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM effective_catalog_items WHERE is_custom AND household_id = $1`, b); n != 0 {
		t.Errorf("B sees %d custom items, want 0", n)
	}
}

func TestDeletingHouseholdRemovesCatalogRows(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	a := insertHousehold(t, pool, "A", nil)
	b := insertHousehold(t, pool, "B", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)
	flour := mustAddItem(t, pool, nil, "Flour", catBaking, locPantry, nil)
	mustAddItem(t, pool, a, "Oat milk", catDairy, locFridge, nil)
	mustAddItem(t, pool, a, "Tahini", catBaking, locPantry, nil)
	mustAddItem(t, pool, b, "Oat milk", catDairy, locFridge, nil)
	setOverride(t, pool, a, milk, catProduce, nil)
	setOverride(t, pool, a, flour, nil, locFreezer)
	setOverride(t, pool, b, milk, nil, locFreezer)

	if _, err := pool.Exec(ctx, `DELETE FROM households WHERE id = $1`, a); err != nil {
		t.Fatalf("deleting household: %v", err)
	}

	checks := []struct {
		what, sql string
		args      []any
		want      int
	}{
		{"A's custom items", `SELECT count(*) FROM catalog_items WHERE household_id = $1`, []any{a}, 0},
		{"A's overrides", `SELECT count(*) FROM household_item_defaults WHERE household_id = $1`, []any{a}, 0},
		{"built-in items", `SELECT count(*) FROM catalog_items WHERE household_id IS NULL`, nil, 2},
		{"B's custom items", `SELECT count(*) FROM catalog_items WHERE household_id = $1`, []any{b}, 1},
		{"B's overrides", `SELECT count(*) FROM household_item_defaults WHERE household_id = $1`, []any{b}, 1},
		{"A's view rows", `SELECT count(*) FROM effective_catalog_items WHERE household_id = $1`, []any{a}, 0},
		{"B's view rows", `SELECT count(*) FROM effective_catalog_items WHERE household_id = $1`, []any{b}, 3},
	}
	for _, c := range checks {
		if n := count(t, pool, c.sql, c.args...); n != c.want {
			t.Errorf("%s: %d rows, want %d", c.what, n, c.want)
		}
	}
}

func TestDeletingBuiltInItemRemovesOverrides(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	a := insertHousehold(t, pool, "A", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)
	setOverride(t, pool, a, milk, catProduce, nil)
	if _, err := pool.Exec(ctx, `DELETE FROM catalog_items WHERE id = $1`, milk); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM household_item_defaults`); n != 0 {
		t.Errorf("%d override rows after deleting the item, want 0", n)
	}
}

// planNode is the part of EXPLAIN (FORMAT JSON) output the plan test reads.
type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	IndexCond    string     `json:"Index Cond"`
	RecheckCond  string     `json:"Recheck Cond"`
	Filter       string     `json:"Filter"`
	JoinFilter   string     `json:"Join Filter"`
	HashCond     string     `json:"Hash Cond"`
	MergeCond    string     `json:"Merge Cond"`
	Plans        []planNode `json:"Plans"`
}

func (n planNode) walk(f func(planNode)) {
	f(n)
	for _, c := range n.Plans {
		c.walk(f)
	}
}

// TestEffectiveCatalogPlanFiltersHouseholdFirst checks that filtering the view
// on one household restricts households and household_item_defaults to that
// household in their scans, rather than filtering the joined cross product.
func TestEffectiveCatalogPlanFiltersHouseholdFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	// 50 households, 30 built-ins, 3 custom items and 5 overrides each.
	builtIns := make([]string, 30)
	for i := range builtIns {
		builtIns[i] = mustAddItem(t, pool, nil, fmt.Sprintf("Built-in %d", i), catBaking, locPantry, nil)
	}
	households := make([]string, 50)
	for i := range households {
		households[i] = insertHousehold(t, pool, fmt.Sprintf("Household %d", i), nil)
		for j := range 3 {
			mustAddItem(t, pool, households[i], fmt.Sprintf("Custom %d", j), catDairy, locFridge, nil)
		}
		for j := range 5 {
			setOverride(t, pool, households[i], builtIns[(i+j)%len(builtIns)], catProduce, nil)
		}
	}
	if _, err := pool.Exec(ctx, `ANALYZE households, catalog_items, household_item_defaults`); err != nil {
		t.Fatal(err)
	}
	target := households[17]

	if n := count(t, pool, `SELECT count(*) FROM effective_catalog_items WHERE household_id = $1`, target); n != 33 {
		t.Fatalf("target household sees %d items, want 33", n)
	}

	cases := []struct {
		name  string
		setup []string
		sql   string
		param string // how the household ID appears in the plan
	}{
		{
			name:  "literal",
			sql:   `EXPLAIN (FORMAT JSON) SELECT * FROM effective_catalog_items WHERE household_id = '` + target + `'`,
			param: target,
		},
		{
			name: "generic plan for $1",
			setup: []string{
				`SET plan_cache_mode = force_generic_plan`,
				`PREPARE eff(uuid) AS SELECT * FROM effective_catalog_items WHERE household_id = $1`,
			},
			sql:   `EXPLAIN (FORMAT JSON) EXECUTE eff('` + target + `')`,
			param: "$1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			defer conn.Exec(ctx, `DEALLOCATE ALL; RESET plan_cache_mode`) //nolint:errcheck
			for _, s := range c.setup {
				if _, err := conn.Exec(ctx, s); err != nil {
					t.Fatalf("%s: %v", s, err)
				}
			}
			var raw []byte
			if err := conn.QueryRow(ctx, c.sql).Scan(&raw); err != nil {
				t.Fatalf("explain: %v", err)
			}
			var out []struct {
				Plan planNode `json:"Plan"`
			}
			if err := json.Unmarshal(raw, &out); err != nil || len(out) != 1 {
				t.Fatalf("parsing plan: %v\n%s", err, raw)
			}
			plan := out[0].Plan

			scanConds := map[string]string{}
			plan.walk(func(n planNode) {
				if n.RelationName != "" {
					scanConds[n.RelationName] += n.IndexCond + " " + n.RecheckCond + " " + n.Filter
				}
				// No join or later node filters on the household.
				if n.RelationName == "" {
					for _, cond := range []string{n.Filter, n.JoinFilter, n.HashCond, n.MergeCond} {
						if strings.Contains(cond, c.param) {
							t.Errorf("%s node filters on the household after a join: %q", n.NodeType, cond)
						}
					}
				}
			})
			for _, rel := range []string{"households", "household_item_defaults"} {
				conds, ok := scanConds[rel]
				if !ok {
					t.Errorf("plan does not scan %s", rel)
					continue
				}
				if !strings.Contains(conds, c.param) {
					t.Errorf("scan of %s is not restricted to the household (conditions: %q)", rel, strings.TrimSpace(conds))
				}
			}
			if t.Failed() {
				t.Logf("plan:\n%s", raw)
			}
		})
	}
}

// TestCatalogMigrationDownUp rolls back this migration (and any later ones),
// checks every object it created is gone, then migrates up again.
func TestCatalogMigrationDownUp(t *testing.T) {
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
		if filepath.Base(s.Path) == catalogMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", catalogMigration)
	}

	type objects struct{ View, Items, Defaults, UniqueIndex, TrgmIndex, Extension bool }
	exist := func() objects {
		t.Helper()
		var o objects
		if err := pool.QueryRow(ctx, `
			SELECT to_regclass('effective_catalog_items') IS NOT NULL,
			       to_regclass('catalog_items') IS NOT NULL,
			       to_regclass('household_item_defaults') IS NOT NULL,
			       to_regclass('catalog_items_household_id_lower_name_key') IS NOT NULL,
			       to_regclass('catalog_items_lower_name_trgm') IS NOT NULL,
			       EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')`,
		).Scan(&o.View, &o.Items, &o.Defaults, &o.UniqueIndex, &o.TrgmIndex, &o.Extension); err != nil {
			t.Fatal(err)
		}
		return o
	}

	if got, want := exist(), (objects{true, true, true, true, true, true}); got != want {
		t.Fatalf("before down: %+v; want all present", got)
	}
	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	if got := exist(); got != (objects{}) {
		t.Fatalf("after down: %+v; want all dropped", got)
	}
	if _, err := provider.UpTo(ctx, this); err != nil {
		t.Fatalf("up to %d: %v", this, err)
	}
	if got, want := exist(), (objects{true, true, true, true, true, true}); got != want {
		t.Fatalf("after up: %+v; want all created", got)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
}
