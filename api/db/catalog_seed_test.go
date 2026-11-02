package db_test

import (
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

// catalogSeedMigration is the file that seeds the built-in catalog (#8).
const catalogSeedMigration = "20261102130000_catalog_seed.sql"

// properNames are the capitalized words allowed after the first word of a
// built-in name, per the naming convention in the seed migration. The seed's
// proper names ("Dijon mustard", "Greek yogurt") are all first words, so the
// set is empty; a later migration that adds one adds it here.
var properNames = map[string]bool{}

type builtIn struct {
	Name          string
	Category      string
	Location      string
	ShelfLifeDays *int32
}

// builtIns returns every built-in item with its category and location keys,
// ordered by name. The inner joins drop rows whose category or location does
// not exist; TestCatalogSeedReferencesExist checks there are none.
func builtIns(t *testing.T, pool *pgxpool.Pool) []builtIn {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT c.name, cat.key, loc.key, c.shelf_life_days
		FROM catalog_items c
		JOIN categories cat ON cat.id = c.default_category_id
		JOIN locations loc ON loc.id = c.default_location_id
		WHERE c.household_id IS NULL
		ORDER BY c.name`)
	if err != nil {
		t.Fatal(err)
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (builtIn, error) {
		var b builtIn
		err := r.Scan(&b.Name, &b.Category, &b.Location, &b.ShelfLifeDays)
		return b, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestCatalogSeedCount(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	n := count(t, pool, `SELECT count(*) FROM catalog_items WHERE household_id IS NULL`)
	if n < 150 || n > 300 {
		t.Errorf("%d built-in items, want 150 to 300", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM catalog_items WHERE household_id IS NULL AND created_by IS NOT NULL`); n != 0 {
		t.Errorf("%d built-in items have created_by set, want 0", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM catalog_items WHERE household_id IS NOT NULL`); n != 0 {
		t.Errorf("%d custom items in a fresh database, want 0", n)
	}
}

func TestCatalogSeedReferencesExist(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	all := count(t, pool, `SELECT count(*) FROM catalog_items WHERE household_id IS NULL`)
	if joined := len(builtIns(t, pool)); joined != all {
		t.Errorf("%d of %d built-in items join to a category and location", joined, all)
	}
}

func TestCatalogSeedNames(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	items := builtIns(t, pool)

	byLower := map[string]string{}
	for _, it := range items {
		if strings.TrimSpace(it.Name) != it.Name {
			t.Errorf("name %q has leading or trailing whitespace", it.Name)
		}
		if strings.Contains(it.Name, "  ") {
			t.Errorf("name %q has a double space", it.Name)
		}
		if it.Name == "" {
			t.Errorf("empty name")
			continue
		}
		// Sentence case: the first word starts with a capital, later words
		// are lower case unless they are proper names.
		words := strings.Fields(it.Name)
		if first := words[0][:1]; first != strings.ToUpper(first) {
			t.Errorf("name %q does not start with a capital", it.Name)
		}
		for _, w := range words[1:] {
			if w != strings.ToLower(w) && !properNames[w] {
				t.Errorf("name %q is not in sentence case (%q)", it.Name, w)
			}
		}
		lower := strings.ToLower(it.Name)
		if prev, ok := byLower[lower]; ok {
			t.Errorf("names %q and %q differ only by case", prev, it.Name)
		}
		byLower[lower] = it.Name
	}

	// No two names that differ only by a plural ending.
	for lower, name := range byLower {
		for _, suffix := range []string{"s", "es"} {
			if other, ok := byLower[lower+suffix]; ok {
				t.Errorf("names %q and %q differ only by plural", name, other)
			}
		}
	}

	// The database agrees: lower(name) is unique among built-ins.
	if n := count(t, pool, `
		SELECT count(*) FROM (
			SELECT lower(name) FROM catalog_items WHERE household_id IS NULL
			GROUP BY lower(name) HAVING count(*) > 1
		) d`); n != 0 {
		t.Errorf("%d lower-case names are used more than once", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM catalog_items WHERE household_id IS NULL AND name <> btrim(name, E' \t\n\r')`); n != 0 {
		t.Errorf("%d names have leading or trailing whitespace", n)
	}
}

func TestCatalogSeedShelfLife(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	for _, it := range builtIns(t, pool) {
		if d := it.ShelfLifeDays; d != nil && (*d < 1 || *d > 3650) {
			t.Errorf("%s: shelf_life_days = %d, want NULL or 1 to 3650", it.Name, *d)
		}
	}
	if n := count(t, pool, `
		SELECT count(*) FROM catalog_items
		WHERE household_id IS NULL AND shelf_life_days IS NOT NULL
		  AND shelf_life_days NOT BETWEEN 1 AND 3650`); n != 0 {
		t.Errorf("%d built-in items have shelf_life_days outside 1 to 3650", n)
	}
}

func TestCatalogSeedSpotChecks(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	byName := map[string]builtIn{}
	for _, it := range builtIns(t, pool) {
		byName[it.Name] = it
	}

	// minDays and maxDays of 0 mean the shelf life must be NULL.
	checks := []struct {
		name      string
		category  string
		locations []string
		minDays   int32
		maxDays   int32
	}{
		{"Milk", "dairy", []string{"fridge"}, 5, 10},
		{"Eggs", "dairy", []string{"fridge"}, 21, 35},
		{"All-purpose flour", "baking", []string{"pantry"}, 180, 365},
		{"Salt", "spices", []string{"spice_rack", "pantry"}, 0, 0},
		{"Bananas", "produce", []string{"counter"}, 3, 7},
		{"Frozen peas", "produce", []string{"freezer"}, 240, 365},
		// Items that in practice do not expire.
		{"Sugar", "baking", []string{"pantry"}, 0, 0},
		{"Honey", "condiments", []string{"pantry"}, 0, 0},
		{"White vinegar", "oils", []string{"pantry"}, 0, 0},
	}
	for _, c := range checks {
		it, ok := byName[c.name]
		if !ok {
			t.Errorf("%s: not in the built-in catalog", c.name)
			continue
		}
		if it.Category != c.category {
			t.Errorf("%s: category %s, want %s", c.name, it.Category, c.category)
		}
		found := false
		for _, l := range c.locations {
			found = found || it.Location == l
		}
		if !found {
			t.Errorf("%s: location %s, want one of %v", c.name, it.Location, c.locations)
		}
		switch d := it.ShelfLifeDays; {
		case c.maxDays == 0 && d != nil:
			t.Errorf("%s: shelf_life_days = %d, want NULL", c.name, *d)
		case c.maxDays != 0 && d == nil:
			t.Errorf("%s: shelf_life_days = NULL, want %d to %d", c.name, c.minDays, c.maxDays)
		case c.maxDays != 0 && (*d < c.minDays || *d > c.maxDays):
			t.Errorf("%s: shelf_life_days = %d, want %d to %d", c.name, *d, c.minDays, c.maxDays)
		}
	}
}

func TestCatalogSeedCoverage(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	type keyCount struct {
		Key   string
		Count int
	}
	perKey := func(sql string) []keyCount {
		t.Helper()
		rows, err := pool.Query(t.Context(), sql)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[keyCount])
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	categories := perKey(`
		SELECT cat.key, count(c.id)::int
		FROM categories cat
		LEFT JOIN catalog_items c ON c.default_category_id = cat.id AND c.household_id IS NULL
		GROUP BY cat.key ORDER BY cat.key`)
	if len(categories) == 0 {
		t.Fatal("no categories")
	}
	for _, c := range categories {
		if c.Key != "other" && c.Count < 5 {
			t.Errorf("category %s has %d built-in items, want at least 5", c.Key, c.Count)
		}
	}

	locations := perKey(`
		SELECT loc.key, count(c.id)::int
		FROM locations loc
		LEFT JOIN catalog_items c ON c.default_location_id = loc.id AND c.household_id IS NULL
		GROUP BY loc.key ORDER BY loc.key`)
	if len(locations) == 0 {
		t.Fatal("no locations")
	}
	for _, l := range locations {
		if l.Count < 1 {
			t.Errorf("location %s has no built-in items, want at least 1", l.Key)
		}
	}
}

// TestCatalogSeedMigrationDownUp rolls back the seed migration (and any later
// ones), checks that exactly the built-in rows are gone and the tables stay,
// then migrates up again and checks the same names come back.
func TestCatalogSeedMigrationDownUp(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	names := func() []string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT name FROM catalog_items WHERE household_id IS NULL ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	before := names()
	if len(before) == 0 {
		t.Fatal("no built-in items before down")
	}

	// A custom item survives the rollback; the seed only owns built-ins.
	home := insertHousehold(t, pool, "Home", nil)
	mustAddItem(t, pool, home, "Grandma's chutney", catBaking, locPantry, nil)

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := db.NewProvider(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	var this, previous int64 = -1, 0
	for _, s := range provider.ListSources() {
		if filepath.Base(s.Path) == catalogSeedMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", catalogSeedMigration)
	}

	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	if got := names(); len(got) != 0 {
		t.Errorf("%d built-in items after down, want 0", len(got))
	}
	for _, tbl := range []string{"catalog_items", "household_item_defaults", "categories", "locations"} {
		var reg *string
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1)::text", "public."+tbl).Scan(&reg); err != nil {
			t.Fatal(err)
		}
		if reg == nil {
			t.Errorf("table %s dropped by down, want it kept", tbl)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM catalog_items WHERE household_id = $1`, home); n != 1 {
		t.Errorf("%d custom items after down, want 1", n)
	}

	if _, err := provider.UpTo(ctx, this); err != nil {
		t.Fatalf("up to %d: %v", this, err)
	}
	if after := names(); !reflect.DeepEqual(after, before) {
		t.Errorf("built-in names after down/up differ: %d before, %d after", len(before), len(after))
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
}
