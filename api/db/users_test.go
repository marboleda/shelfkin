package db_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
)

// usersMigration is the file that creates users and push_tokens.
const usersMigration = "20261029115345_users_push_tokens.sql"

const (
	sqlStateNotNull    = "23502"
	sqlStateForeignKey = "23503"
	sqlStateUnique     = "23505"
)

type column struct {
	Name     string
	Type     string
	Nullable bool
	Default  string
}

func tableColumns(t *testing.T, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, table string) []column {
	t.Helper()
	rows, err := q.Query(t.Context(), `
		SELECT a.attname,
		       format_type(a.atttypid, a.atttypmod),
		       NOT a.attnotnull,
		       coalesce(pg_get_expr(d.adbin, d.adrelid), '')
		FROM pg_attribute a
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE a.attrelid = to_regclass($1) AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, table)
	if err != nil {
		t.Fatalf("listing columns of %s: %v", table, err)
	}
	cols, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (column, error) {
		var c column
		err := r.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default)
		return c, err
	})
	if err != nil {
		t.Fatalf("listing columns of %s: %v", table, err)
	}
	return cols
}

func TestUsersAndPushTokensColumns(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	tests := map[string][]column{
		"users": {
			{"id", "uuid", false, "uuidv7()"},
			{"firebase_uid", "text", false, ""},
			{"display_name", "text", false, ""},
			{"email", "text", true, ""},
			{"timezone", "text", false, ""},
			{"push_enabled", "boolean", false, "false"},
			{"created_at", "timestamp with time zone", false, "now()"},
			{"updated_at", "timestamp with time zone", false, "now()"},
		},
		"push_tokens": {
			{"id", "uuid", false, "uuidv7()"},
			{"user_id", "uuid", false, ""},
			{"token", "text", false, ""},
			{"platform", "text", true, ""},
			{"created_at", "timestamp with time zone", false, "now()"},
			{"last_seen_at", "timestamp with time zone", false, "now()"},
		},
	}
	for table, want := range tests {
		if got := tableColumns(t, pool, table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s columns:\n got  %+v\n want %+v", table, got, want)
		}
	}
}

func TestUsersAndPushTokensConstraints(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	rows, err := pool.Query(t.Context(), `
		SELECT conrelid::regclass::text, pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid IN ('users'::regclass, 'push_tokens'::regclass)
		  AND contype <> 'n' -- NOT NULL (Postgres 18+) is checked by the columns test
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]string, error) {
		var c [2]string
		err := r.Scan(&c[0], &c[1])
		return c, err
	})
	if err != nil {
		t.Fatal(err)
	}
	// No CHECK constraints (platform is validated by the API) and no unique
	// constraint on email or on (user_id, token).
	want := [][2]string{
		{"push_tokens", "FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE"},
		{"push_tokens", "PRIMARY KEY (id)"},
		{"push_tokens", "UNIQUE (token)"},
		{"users", "PRIMARY KEY (id)"},
		{"users", "UNIQUE (firebase_uid)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("constraints:\n got  %v\n want %v", got, want)
	}
}

func insertUser(t *testing.T, pool *pgxpool.Pool, firebaseUID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO users (firebase_uid, display_name, timezone) VALUES ($1, 'Sam', 'America/Toronto') RETURNING id`,
		firebaseUID).Scan(&id)
	if err != nil {
		t.Fatalf("inserting user %s: %v", firebaseUID, err)
	}
	return id
}

func insertToken(ctx context.Context, pool *pgxpool.Pool, userID, token string) error {
	_, err := pool.Exec(ctx, `INSERT INTO push_tokens (user_id, token, platform) VALUES ($1, $2, 'ios')`, userID, token)
	return err
}

func wantSQLState(t *testing.T, err error, code, what string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: err = %v, want SQLSTATE %s", what, err, code)
	}
	if pgErr.Code != code {
		t.Fatalf("%s: SQLSTATE = %s (%s), want %s", what, pgErr.Code, pgErr.Message, code)
	}
}

func TestUserAndTokenDefaults(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	before := time.Now().Add(-time.Minute)

	var (
		userID              string
		userVersion         int
		pushEnabled         bool
		email               *string
		createdAt, updateAt time.Time
	)
	err := pool.QueryRow(ctx, `
		INSERT INTO users (firebase_uid, display_name, timezone)
		VALUES ('fb-1', 'Sam', 'America/Toronto')
		RETURNING id, uuid_extract_version(id), push_enabled, email, created_at, updated_at`,
	).Scan(&userID, &userVersion, &pushEnabled, &email, &createdAt, &updateAt)
	if err != nil {
		t.Fatalf("inserting user: %v", err)
	}
	if userVersion != 7 {
		t.Errorf("users.id version = %d, want 7", userVersion)
	}
	if pushEnabled {
		t.Error("users.push_enabled defaulted to true, want false")
	}
	if email != nil {
		t.Errorf("users.email = %q, want NULL", *email)
	}
	if createdAt.Before(before) || updateAt.Before(before) {
		t.Errorf("users timestamps not defaulted to now: created_at %v, updated_at %v", createdAt, updateAt)
	}

	var (
		tokenVersion         int
		tokenUser            string
		platform             *string
		tokCreated, lastSeen time.Time
	)
	err = pool.QueryRow(ctx, `
		INSERT INTO push_tokens (user_id, token) VALUES ($1, 'ExponentPushToken[a]')
		RETURNING uuid_extract_version(id), user_id, platform, created_at, last_seen_at`, userID,
	).Scan(&tokenVersion, &tokenUser, &platform, &tokCreated, &lastSeen)
	if err != nil {
		t.Fatalf("inserting push token: %v", err)
	}
	if tokenVersion != 7 {
		t.Errorf("push_tokens.id version = %d, want 7", tokenVersion)
	}
	if tokenUser != userID {
		t.Errorf("push_tokens.user_id = %s, want %s", tokenUser, userID)
	}
	if platform != nil {
		t.Errorf("push_tokens.platform = %q, want NULL", *platform)
	}
	if tokCreated.Before(before) || lastSeen.Before(before) {
		t.Errorf("push_tokens timestamps not defaulted to now: created_at %v, last_seen_at %v", tokCreated, lastSeen)
	}
}

func TestUsersRequiredColumns(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	_, err := pool.Exec(t.Context(), `INSERT INTO users (firebase_uid, timezone) VALUES ('fb-1', 'UTC')`)
	wantSQLState(t, err, sqlStateNotNull, "user without display_name")
	_, err = pool.Exec(t.Context(), `INSERT INTO users (firebase_uid, display_name) VALUES ('fb-2', 'Sam')`)
	wantSQLState(t, err, sqlStateNotNull, "user without timezone")
	_, err = pool.Exec(t.Context(), `INSERT INTO users (display_name, timezone) VALUES ('Sam', 'UTC')`)
	wantSQLState(t, err, sqlStateNotNull, "user without firebase_uid")
}

func TestUsersFirebaseUIDUnique(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	insertUser(t, pool, "fb-1")

	_, err := pool.Exec(t.Context(),
		`INSERT INTO users (firebase_uid, display_name, timezone) VALUES ('fb-1', 'Alex', 'UTC')`)
	wantSQLState(t, err, sqlStateUnique, "duplicate firebase_uid")
}

func TestUsersEmailNotUnique(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	for i, email := range []any{"sam@example.com", "sam@example.com", nil, nil} {
		_, err := pool.Exec(t.Context(),
			`INSERT INTO users (firebase_uid, display_name, timezone, email) VALUES ($1, 'Sam', 'UTC', $2)`,
			"fb-"+string(rune('a'+i)), email)
		if err != nil {
			t.Fatalf("inserting user %d with email %v: %v", i, email, err)
		}
	}
}

func TestPushTokensTokenUniqueAcrossUsers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")

	if err := insertToken(ctx, pool, sam, "tok-1"); err != nil {
		t.Fatal(err)
	}
	wantSQLState(t, insertToken(ctx, pool, sam, "tok-1"), sqlStateUnique, "same token, same user")
	wantSQLState(t, insertToken(ctx, pool, alex, "tok-1"), sqlStateUnique, "same token, different user")
}

func TestPushTokensSeveralPerUser(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")

	for _, tok := range []string{"phone", "tablet", "web"} {
		if err := insertToken(ctx, pool, sam, tok); err != nil {
			t.Fatalf("inserting token %s: %v", tok, err)
		}
	}
	// platform is free text: anything, or nothing, is accepted.
	if _, err := pool.Exec(ctx, `INSERT INTO push_tokens (user_id, token, platform) VALUES ($1, 'other', 'not-a-platform')`, sam); err != nil {
		t.Fatalf("inserting token with unlisted platform: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM push_tokens WHERE user_id = $1`, sam).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("user has %d tokens, want 4", n)
	}
}

func TestPushTokensUnknownUser(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	err := insertToken(t.Context(), pool, "0199a000-0000-7000-8000-000000000000", "tok-1")
	wantSQLState(t, err, sqlStateForeignKey, "token for missing user")
}

func TestDeletingUserCascadesToOwnTokensOnly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	for _, tok := range []string{"sam-phone", "sam-web"} {
		if err := insertToken(ctx, pool, sam, tok); err != nil {
			t.Fatal(err)
		}
	}
	if err := insertToken(ctx, pool, alex, "alex-phone"); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, sam); err != nil {
		t.Fatalf("deleting user: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT token FROM push_tokens ORDER BY token`)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tokens, []string{"alex-phone"}) {
		t.Errorf("tokens after deleting sam = %v, want [alex-phone]", tokens)
	}
}

// TestUsersMigrationDownUp rolls back to just before this migration (which
// also reverts any later ones), checks both tables are gone, then migrates up
// again.
func TestUsersMigrationDownUp(t *testing.T) {
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
		if filepath.Base(s.Path) == usersMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", usersMigration)
	}

	tablesExist := func() (users, tokens bool) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT to_regclass('users') IS NOT NULL, to_regclass('push_tokens') IS NOT NULL`,
		).Scan(&users, &tokens); err != nil {
			t.Fatal(err)
		}
		return users, tokens
	}

	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	if u, p := tablesExist(); u || p {
		t.Fatalf("after down: users exists = %v, push_tokens exists = %v; want both dropped", u, p)
	}
	if _, err := provider.UpTo(ctx, this); err != nil {
		t.Fatalf("up to %d: %v", this, err)
	}
	if u, p := tablesExist(); !u || !p {
		t.Fatalf("after up: users exists = %v, push_tokens exists = %v; want both created", u, p)
	}
	if got := tableColumns(t, pool, "push_tokens"); len(got) != 6 {
		t.Errorf("push_tokens after re-up has %d columns, want 6", len(got))
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
}
