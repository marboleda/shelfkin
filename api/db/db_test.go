package db_test

import (
	"io/fs"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
)

// goose timestamp versions keep parallel branches from picking the same number.
var migrationName = regexp.MustCompile(`^\d{14}_[a-z0-9_]+\.sql$`)

func TestMigrationFileNames(t *testing.T) {
	entries, err := fs.ReadDir(db.Migrations(), ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no migrations embedded")
	}
	for _, e := range entries {
		if !migrationName.MatchString(e.Name()) {
			t.Errorf("migration %q does not match YYYYMMDDHHMMSS_name.sql", e.Name())
		}
	}
}

// TestMigrationsUpDownUp runs every migration's Down section, so each new
// migration's rollback is exercised automatically.
func TestMigrationsUpDownUp(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t) // already migrated up

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := db.NewProvider(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	sources := provider.ListSources()
	latest := sources[len(sources)-1].Version

	if v, err := provider.GetDBVersion(ctx); err != nil || v != latest {
		t.Fatalf("after dbtest.New: version = %d, %v; want %d", v, err, latest)
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if v, err := provider.GetDBVersion(ctx); err != nil || v != 0 {
		t.Fatalf("after down: version = %d, %v; want 0", v, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if v, err := provider.GetDBVersion(ctx); err != nil || v != latest {
		t.Fatalf("after up again: version = %d, %v; want %d", v, err, latest)
	}
}
