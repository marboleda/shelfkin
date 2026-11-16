package db_test

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/marboleda/shelfkin/api/db/dbtest"
)

// TestDeleteAccountFunction checks delete_account is SECURITY DEFINER with a
// fixed empty search_path, and that shelfkin_app can execute it but PUBLIC
// cannot.
func TestDeleteAccountFunction(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	var (
		secdef, appExec, publicExec bool
		config, grantees            []string
		result                      string
	)
	err := pool.QueryRow(t.Context(), `
		SELECT p.prosecdef, p.proconfig, pg_get_function_result(p.oid),
		       has_function_privilege('shelfkin_app', p.oid, 'EXECUTE'),
		       EXISTS (SELECT 1 FROM aclexplode(p.proacl) a WHERE a.grantee = 0),
		       ARRAY(SELECT a.grantee::regrole::text || ':' || a.privilege_type
		             FROM aclexplode(p.proacl) a
		             WHERE a.grantee <> p.proowner ORDER BY 1)
		FROM pg_proc p
		WHERE p.proname = 'delete_account' AND p.pronamespace = 'public'::regnamespace
		  AND pg_get_function_identity_arguments(p.oid) = ''`,
	).Scan(&secdef, &config, &result, &appExec, &publicExec, &grantees)
	if err != nil {
		t.Fatalf("delete_account(): %v", err)
	}
	if !secdef {
		t.Error("delete_account is not SECURITY DEFINER")
	}
	if !reflect.DeepEqual(config, []string{`search_path=""`}) {
		t.Errorf("delete_account config = %q, want [search_path=\"\"]", config)
	}
	if want := "TABLE(household_id uuid, household_name text)"; result != want {
		t.Errorf("delete_account returns %s, want %s", result, want)
	}
	if !appExec || publicExec {
		t.Errorf("shelfkin_app execute %v, PUBLIC execute %v; want true, false", appExec, publicExec)
	}
	if want := []string{"shelfkin_app:EXECUTE"}; !reflect.DeepEqual(grantees, want) {
		t.Errorf("grants on delete_account = %q, want %q", grantees, want)
	}
}

// TestDeleteAccountRejected: with no app.user_id, or for a user that does not
// exist, delete_account fails and deletes nothing. shelfkin_app cannot delete
// a users row or write deleted_accounts itself.
func TestDeleteAccountRejected(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	home := insertHousehold(t, pool, "Home", sam)
	mustInsertMember(t, pool, home, sam, "owner")

	call := func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `SELECT * FROM delete_account()`)
		return err
	}
	wantDenied(t, inTx(t, pool, "", call), "app.user_id is not set", "delete_account with no user")
	_, err := pool.Exec(t.Context(), `SELECT * FROM delete_account()`)
	wantDenied(t, err, "app.user_id is not set", "delete_account outside dbtx")
	wantSQLState(t, inTx(t, pool, "0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80", call), "P0002", "delete_account for no user")

	err = inTx(t, pool, sam, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `DELETE FROM users WHERE id = $1`, sam)
		return err
	})
	wantDenied(t, err, permissionDenied, "shelfkin_app deletes a users row")
	err = inTx(t, pool, sam, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `INSERT INTO deleted_accounts (firebase_uid) VALUES ('someone')`)
		return err
	})
	wantDenied(t, err, permissionDenied, "shelfkin_app writes deleted_accounts")

	if n := count(t, pool, `SELECT count(*) FROM users WHERE id = $1`, sam); n != 1 {
		t.Errorf("users rows for Sam: %d, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM households WHERE id = $1`, home); n != 1 {
		t.Errorf("Home: %d rows, want 1", n)
	}
}

// TestDeleteAccountAsAppRole runs delete_account through dbtx for a sole
// owner who is also a member of another household: their own household goes,
// the other stays without them, and the UID is recorded.
func TestDeleteAccountAsAppRole(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	flat := insertHousehold(t, pool, "Flat", sam)
	mustInsertMember(t, pool, flat, sam, "owner")
	home := insertHousehold(t, pool, "Home", sam)
	mustInsertMember(t, pool, home, alex, "owner")
	mustInsertMember(t, pool, home, sam, "member")

	var blocking int
	err := inTxCommit(t, pool, sam, func(tx pgx.Tx) error {
		rows, err := tx.Query(t.Context(), `SELECT * FROM delete_account()`)
		if err != nil {
			return err
		}
		for rows.Next() {
			blocking++
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("delete_account: %v", err)
	}
	if blocking != 0 {
		t.Errorf("delete_account returned %d households, want none", blocking)
	}
	if n := count(t, pool, `SELECT count(*) FROM users WHERE id = $1`, sam); n != 0 {
		t.Errorf("users rows for Sam: %d, want 0", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM households WHERE id = $1`, flat); n != 0 {
		t.Errorf("Flat: %d rows, want 0", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM households WHERE id = $1 AND created_by IS NULL`, home); n != 1 {
		t.Errorf("Home (created by Sam, owned by Alex): want it kept with NULL created_by")
	}
	if n := count(t, pool, `SELECT count(*) FROM household_members WHERE household_id = $1`, home); n != 1 {
		t.Errorf("Home members: %d, want 1 (Alex)", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM deleted_accounts WHERE firebase_uid = 'fb-sam'`); n != 1 {
		t.Errorf("deleted_accounts has no row for fb-sam")
	}
}
