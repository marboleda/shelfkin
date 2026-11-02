// Package dbtx opens the database transactions the API runs in.
//
// All API database access must go through WithUser or WithoutUser. Each
// transaction switches to the restricted shelfkin_app role (SET LOCAL ROLE)
// and, for WithUser, sets app.user_id for that transaction only, so the
// row-level security policies (docs/data-model.md, Access control) limit every
// query to the caller's households even if it forgets a household filter.
//
// The pool's own connection user (a superuser locally) bypasses those
// policies. Using it directly is only for migrations and tests.
package dbtx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// AppRole is the restricted database role every API transaction runs as.
const AppRole = "shelfkin_app"

// Beginner starts a transaction. *pgxpool.Pool, *pgxpool.Conn and *pgx.Conn
// satisfy it.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ErrNoUser is returned by WithUser when userID is not a valid UUID. Use
// WithoutUser for a transaction that deliberately has no user.
var ErrNoUser = errors.New("dbtx: user ID is not set")

// WithUser runs fn in a transaction as shelfkin_app with app.user_id set to
// userID. It commits if fn returns nil and rolls back if fn returns an error
// or panics (the panic is re-raised after the rollback).
func WithUser(ctx context.Context, db Beginner, userID pgtype.UUID, fn func(pgx.Tx) error) error {
	if !userID.Valid {
		return ErrNoUser
	}
	return run(ctx, db, &userID, fn)
}

// WithoutUser runs fn in a transaction as shelfkin_app with no user, for work
// that happens before the caller's users.id is known (looking up or creating
// the user on first sight, #13). Household tables return no rows in it.
// Commit and rollback work as in WithUser.
func WithoutUser(ctx context.Context, db Beginner, fn func(pgx.Tx) error) error {
	return run(ctx, db, nil, fn)
}

func run(ctx context.Context, db Beginner, userID *pgtype.UUID, fn func(pgx.Tx) error) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("dbtx: begin: %w", err)
	}
	// The rollback uses a context that is not cancelled with ctx, so a
	// cancelled request still ends its transaction cleanly. Rollback after a
	// successful Commit is a no-op.
	rollback := func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }
	defer func() {
		if p := recover(); p != nil {
			rollback()
			panic(p)
		}
		if err != nil {
			rollback()
		}
	}()

	if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+AppRole); err != nil {
		return fmt.Errorf("dbtx: set role: %w", err)
	}
	if userID != nil {
		// SET LOCAL cannot take a bind parameter; set_config(..., true) is the
		// transaction-local equivalent that can.
		if _, err = tx.Exec(ctx, "SELECT set_config('app.user_id', $1::uuid::text, true)", *userID); err != nil {
			return fmt.Errorf("dbtx: set user: %w", err)
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("dbtx: commit: %w", err)
	}
	return nil
}
