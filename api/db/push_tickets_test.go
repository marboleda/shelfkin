package db_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
)

// pushTicketsMigration creates push_tickets (#47).
const pushTicketsMigration = "20261114100000_push_tickets.sql"

func TestPushTicketsTable(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	want := []column{
		{"id", "text", false, ""},
		{"token_id", "uuid", false, ""},
		{"sent_at", "timestamp with time zone", false, "now()"},
	}
	if got := tableColumns(t, pool, "push_tickets"); !reflect.DeepEqual(got, want) {
		t.Errorf("push_tickets columns:\n got  %+v\n want %+v", got, want)
	}

	var tokenID string
	if err := pool.QueryRow(ctx, `
		WITH u AS (
			INSERT INTO users (firebase_uid, display_name, timezone)
			VALUES ('fb-1', 'Sam', 'UTC') RETURNING id
		)
		INSERT INTO push_tokens (user_id, token) SELECT id, 'ExponentPushToken[abc]' FROM u
		RETURNING id::text`).Scan(&tokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO push_tickets (id, token_id) VALUES ('a', $1), ('b', $1)`, tokenID); err != nil {
		t.Fatal(err)
	}

	// The ticket ID is the key, and the token must exist.
	_, err := pool.Exec(ctx, `INSERT INTO push_tickets (id, token_id) VALUES ('a', $1)`, tokenID)
	if pgErr := (*pgconn.PgError)(nil); !errors.As(err, &pgErr) || pgErr.Code != sqlStateUnique {
		t.Errorf("duplicate ticket ID: %v, want unique violation", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO push_tickets (id, token_id) VALUES ('c', uuidv7())`)
	if pgErr := (*pgconn.PgError)(nil); !errors.As(err, &pgErr) || pgErr.Code != sqlStateForeignKey {
		t.Errorf("unknown token: %v, want foreign key violation", err)
	}

	// Deleting the token deletes its tickets.
	if _, err := pool.Exec(ctx, `DELETE FROM push_tokens WHERE id = $1`, tokenID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM push_tickets`); n != 0 {
		t.Errorf("%d tickets left after deleting their token, want 0", n)
	}

	var indexes []string
	rows, err := pool.Query(ctx, `SELECT indexname FROM pg_indexes WHERE tablename = 'push_tickets' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		indexes = append(indexes, name)
	}
	if want := []string{"push_tickets_pkey", "push_tickets_sent_at", "push_tickets_token_id"}; !reflect.DeepEqual(indexes, want) {
		t.Errorf("indexes %q, want %q", indexes, want)
	}
}

// TestPushTicketsMigrationDownUp: down drops the table and leaves push_tokens
// alone; up recreates it.
func TestPushTicketsMigrationDownUp(t *testing.T) {
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
		if filepath.Base(s.Path) == pushTicketsMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", pushTicketsMigration)
	}
	tokens := pushTokensIndexes(t, pool)

	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM pg_class WHERE relname = 'push_tickets'`); n != 0 {
		t.Error("push_tickets still exists after down")
	}
	if got := pushTokensIndexes(t, pool); !reflect.DeepEqual(got, tokens) {
		t.Errorf("push_tokens indexes after down:\n got  %q\n want %q", got, tokens)
	}

	if _, err := provider.UpTo(ctx, this); err != nil {
		t.Fatalf("up to %d: %v", this, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM pg_class WHERE relname = 'push_tickets'`); n != 1 {
		t.Error("push_tickets missing after up")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
}
