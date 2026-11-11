package db_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
)

// pushTokensIndexMigration adds the push_tokens (user_id) index (#21).
const pushTokensIndexMigration = "20261104200000_push_tokens_user_id_index.sql"

const pushTokensUserIDIndex = "CREATE INDEX push_tokens_user_id ON public.push_tokens USING btree (user_id)"

// pushTokensIndexes lists every index on push_tokens, including the primary
// key and the unique token constraint.
func pushTokensIndexes(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT pg_get_indexdef(indexrelid)
		FROM pg_index
		WHERE indrelid = 'push_tokens'::regclass
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPushTokensIndexes(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	want := []string{
		pushTokensUserIDIndex,
		"CREATE UNIQUE INDEX push_tokens_pkey ON public.push_tokens USING btree (id)",
		"CREATE UNIQUE INDEX push_tokens_token_key ON public.push_tokens USING btree (token)",
	}
	if got := pushTokensIndexes(t, pool); !reflect.DeepEqual(got, want) {
		t.Errorf("push_tokens indexes:\n got  %q\n want %q", got, want)
	}
}

// TestPushTokensIndexMigrationDownUp: down drops only the index (the table,
// its rows and its other indexes stay), and up recreates it.
func TestPushTokensIndexMigrationDownUp(t *testing.T) {
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
		if filepath.Base(s.Path) == pushTokensIndexMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", pushTokensIndexMigration)
	}

	if _, err := pool.Exec(ctx, `
		WITH u AS (
			INSERT INTO users (firebase_uid, display_name, timezone)
			VALUES ('fb-1', 'Sam', 'UTC') RETURNING id
		)
		INSERT INTO push_tokens (user_id, token, platform)
		SELECT id, 'ExponentPushToken[abc]', 'ios' FROM u`); err != nil {
		t.Fatal(err)
	}
	before := pushTokensIndexes(t, pool)
	columns := tableColumns(t, pool, "push_tokens")

	// Rolling back to the previous version also reverts any later
	// migrations; none of them may touch push_tokens's indexes.
	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	var withoutIndex []string
	for _, def := range before {
		if def != pushTokensUserIDIndex {
			withoutIndex = append(withoutIndex, def)
		}
	}
	if got := pushTokensIndexes(t, pool); !reflect.DeepEqual(got, withoutIndex) {
		t.Errorf("indexes after down:\n got  %q\n want %q", got, withoutIndex)
	}
	if got := tableColumns(t, pool, "push_tokens"); !reflect.DeepEqual(got, columns) {
		t.Errorf("columns after down = %+v, want unchanged %+v", got, columns)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM push_tokens").Scan(&n); err != nil || n != 1 {
		t.Errorf("rows after down = %d, %v; want 1", n, err)
	}

	if _, err := provider.UpTo(ctx, this); err != nil {
		t.Fatalf("up to %d: %v", this, err)
	}
	if got := pushTokensIndexes(t, pool); !reflect.DeepEqual(got, before) {
		t.Errorf("indexes after up:\n got  %q\n want %q", got, before)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
}
