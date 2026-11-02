package dbtx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
)

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// household inserts a user who owns a household (as the superuser) and
// returns the user's ID.
func household(t *testing.T, q querier) pgtype.UUID {
	t.Helper()
	var user pgtype.UUID
	err := q.QueryRow(t.Context(), `
		WITH u AS (
		    INSERT INTO users (firebase_uid, display_name, timezone)
		    VALUES ('fb-' || uuidv7(), 'Sam', 'UTC') RETURNING id
		), h AS (
		    INSERT INTO households (name) VALUES ('Home') RETURNING id
		)
		INSERT INTO household_members (household_id, user_id, role)
		SELECT h.id, u.id, 'owner' FROM h, u
		RETURNING user_id`).Scan(&user)
	if err != nil {
		t.Fatalf("inserting household: %v", err)
	}
	return user
}

func countUsers(t *testing.T, pool *pgxpool.Pool, firebaseUID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE firebase_uid = $1`, firebaseUID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func insertUser(tx pgx.Tx, ctx context.Context, firebaseUID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO users (firebase_uid, display_name, timezone) VALUES ($1, 'Sam', 'UTC')`, firebaseUID)
	return err
}

func TestWithUserSetsRoleAndUser(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	user := household(t, pool)

	var role, setting string
	var households int
	err := dbtx.WithUser(ctx, pool, user, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_user::text, current_setting('app.user_id'), (SELECT count(*) FROM households)`,
		).Scan(&role, &setting, &households)
	})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := user.Value()
	if role != dbtx.AppRole || setting != want || households != 1 {
		t.Errorf("in WithUser: role %q, app.user_id %q, %d households; want %q, %q, 1",
			role, setting, households, dbtx.AppRole, want)
	}
}

func TestWithoutUserSetsRoleOnly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	household(t, pool)

	var role string
	var setting *string
	var households int
	err := dbtx.WithoutUser(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_user::text, NULLIF(current_setting('app.user_id', true), ''), (SELECT count(*) FROM households)`,
		).Scan(&role, &setting, &households)
	})
	if err != nil {
		t.Fatal(err)
	}
	if role != dbtx.AppRole || setting != nil || households != 0 {
		t.Errorf("in WithoutUser: role %q, app.user_id %v, %d households; want %q, unset, 0",
			role, setting, households, dbtx.AppRole)
	}
}

func TestWithUserRejectsInvalidUser(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	called := false
	err := dbtx.WithUser(t.Context(), pool, pgtype.UUID{}, func(pgx.Tx) error {
		called = true
		return nil
	})
	if !errors.Is(err, dbtx.ErrNoUser) || called {
		t.Errorf("WithUser with an invalid UUID: err %v, callback called %v; want ErrNoUser, false", err, called)
	}
}

func TestCommitOnNil(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	if err := dbtx.WithoutUser(t.Context(), pool, func(tx pgx.Tx) error {
		return insertUser(tx, t.Context(), "fb-commit")
	}); err != nil {
		t.Fatal(err)
	}
	if n := countUsers(t, pool, "fb-commit"); n != 1 {
		t.Errorf("%d users after a committed transaction, want 1", n)
	}
}

func TestRollbackOnError(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	user := household(t, pool)
	errBoom := errors.New("boom")

	err := dbtx.WithUser(t.Context(), pool, user, func(tx pgx.Tx) error {
		if err := insertUser(tx, t.Context(), "fb-rollback"); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Errorf("err = %v, want the callback's error", err)
	}
	if n := countUsers(t, pool, "fb-rollback"); n != 0 {
		t.Errorf("%d users after the callback failed, want 0 (rolled back)", n)
	}
}

func TestRollbackOnPanic(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	user := household(t, pool)

	recovered := func() (p any) {
		defer func() { p = recover() }()
		_ = dbtx.WithUser(t.Context(), pool, user, func(tx pgx.Tx) error {
			if err := insertUser(tx, t.Context(), "fb-panic"); err != nil {
				t.Errorf("insert: %v", err)
			}
			panic("boom")
		})
		return nil
	}()
	if recovered != "boom" {
		t.Errorf("recovered %v, want the callback's panic re-raised", recovered)
	}
	if n := countUsers(t, pool, "fb-panic"); n != 0 {
		t.Errorf("%d users after the callback panicked, want 0 (rolled back)", n)
	}
	// The connection went back to the pool usable.
	if err := dbtx.WithoutUser(t.Context(), pool, func(pgx.Tx) error { return nil }); err != nil {
		t.Errorf("transaction after the panic: %v", err)
	}
}

// TestPooledConnectionReuse runs a transaction for a user and then one with no
// user on the same connection. After the first, app.user_id reads as ” on
// that connection rather than being unset; the policies must treat that as no
// user instead of failing to cast it to uuid.
func TestPooledConnectionReuse(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	user := household(t, conn)

	count := func(tx pgx.Tx) (int, error) {
		var n int
		err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM households) + (SELECT count(*) FROM household_members)`).Scan(&n)
		return n, err
	}

	var first int
	if err := dbtx.WithUser(ctx, conn, user, func(tx pgx.Tx) (err error) {
		first, err = count(tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if first != 2 {
		t.Fatalf("as the user: %d rows, want 2 (household and membership)", first)
	}

	var second int
	var setting *string
	if err := dbtx.WithoutUser(ctx, conn, func(tx pgx.Tx) (err error) {
		if err := tx.QueryRow(ctx, `SELECT current_setting('app.user_id', true)`).Scan(&setting); err != nil {
			return err
		}
		second, err = count(tx)
		return err
	}); err != nil {
		t.Fatalf("no-user transaction on the reused connection: %v", err)
	}
	if setting == nil || *setting != "" {
		t.Errorf("app.user_id on the reused connection = %v, want '' (the case NULLIF guards against)", setting)
	}
	if second != 0 {
		t.Errorf("no-user transaction on the reused connection sees %d of the user's rows, want 0", second)
	}

	// SET LOCAL ROLE ended with the transaction.
	var role string
	if err := conn.QueryRow(ctx, `SELECT current_user::text`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role == dbtx.AppRole {
		t.Errorf("connection still runs as %s after the transaction", role)
	}
}
