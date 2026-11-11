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

// transferOwnershipMigration adds the transfer_ownership function (#15).
const transferOwnershipMigration = "20261105300000_transfer_ownership.sql"

// transferFixture is a household owned by owner with members m1 and m2, and
// an outsider who is in no household.
type transferFixture struct {
	pool                    *pgxpool.Pool
	household               string
	owner, m1, m2, outsider string
}

func newTransferFixture(t *testing.T) *transferFixture {
	t.Helper()
	pool := dbtest.New(t)
	f := &transferFixture{
		pool:     pool,
		owner:    insertUser(t, pool, "owner"),
		m1:       insertUser(t, pool, "m1"),
		m2:       insertUser(t, pool, "m2"),
		outsider: insertUser(t, pool, "outsider"),
	}
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO households (name, created_by) VALUES ('Home', $1) RETURNING id::text`, f.owner,
	).Scan(&f.household); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO household_members (household_id, user_id, role)
		VALUES ($1, $2, 'owner'), ($1, $3, 'member'), ($1, $4, 'member')`,
		f.household, f.owner, f.m1, f.m2); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *transferFixture) owners(t *testing.T) []string {
	t.Helper()
	rows, err := f.pool.Query(t.Context(),
		`SELECT user_id::text FROM household_members WHERE household_id = $1 AND role = 'owner'`, f.household)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// transferAs calls transfer_ownership as user ("" for no user) through dbtx
// and commits if it succeeds.
func (f *transferFixture) transferAs(t *testing.T, user, newOwner string) error {
	t.Helper()
	call := func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `SELECT transfer_ownership($1, $2)`, f.household, newOwner)
		return err
	}
	if user == "" {
		return inTx(t, f.pool, "", call)
	}
	return inTxCommit(t, f.pool, user, call)
}

// TestTransferOwnershipFunction checks transfer_ownership is SECURITY DEFINER
// with a fixed empty search_path, and that shelfkin_app can execute it but
// PUBLIC cannot.
func TestTransferOwnershipFunction(t *testing.T) {
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
		WHERE p.proname = 'transfer_ownership' AND p.pronamespace = 'public'::regnamespace
		  AND pg_get_function_identity_arguments(p.oid) = 'household_id uuid, new_owner_id uuid'`,
	).Scan(&secdef, &config, &result, &appExec, &publicExec, &grantees)
	if err != nil {
		t.Fatalf("transfer_ownership(household_id uuid, new_owner_id uuid): %v", err)
	}
	if !secdef {
		t.Error("transfer_ownership is not SECURITY DEFINER")
	}
	if !reflect.DeepEqual(config, []string{`search_path=""`}) {
		t.Errorf("transfer_ownership config = %q, want [search_path=\"\"]", config)
	}
	if result != "void" {
		t.Errorf("transfer_ownership returns %s, want void", result)
	}
	if !appExec || publicExec {
		t.Errorf("shelfkin_app execute %v, PUBLIC execute %v; want true, false", appExec, publicExec)
	}
	if want := []string{"shelfkin_app:EXECUTE"}; !reflect.DeepEqual(grantees, want) {
		t.Errorf("grants on transfer_ownership = %q, want %q", grantees, want)
	}
}

// TestTransferOwnershipByOwner checks the owner can hand ownership to a
// member as shelfkin_app, and that afterwards there is exactly one owner.
func TestTransferOwnershipByOwner(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)

	if err := f.transferAs(t, f.owner, f.m1); err != nil {
		t.Fatalf("owner transfers to m1: %v", err)
	}
	if got := f.owners(t); !reflect.DeepEqual(got, []string{f.m1}) {
		t.Errorf("owners = %v, want [%s]", got, f.m1)
	}
	if n := count(t, f.pool, `SELECT count(*) FROM household_members WHERE household_id = $1 AND user_id = $2 AND role = 'member'`,
		f.household, f.owner); n != 1 {
		t.Error("the old owner is not a member")
	}
	// The old owner is now a member and cannot transfer again; the new owner can.
	wantSQLState(t, f.transferAs(t, f.owner, f.m2), "42501", "old owner transfers")
	if err := f.transferAs(t, f.m1, f.owner); err != nil {
		t.Fatalf("new owner transfers back: %v", err)
	}
	if got := f.owners(t); !reflect.DeepEqual(got, []string{f.owner}) {
		t.Errorf("owners after transferring back = %v, want [%s]", got, f.owner)
	}
}

// TestTransferOwnershipRejected checks every way the function refuses, and
// that each leaves the owner unchanged.
func TestTransferOwnershipRejected(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)

	// As a member, to another member or to themselves.
	err := f.transferAs(t, f.m1, f.m2)
	wantDenied(t, err, "not the owner", "member transfers to another member")
	err = f.transferAs(t, f.m1, f.m1)
	wantDenied(t, err, "not the owner", "member transfers to themselves")
	// As an outsider.
	err = f.transferAs(t, f.outsider, f.outsider)
	wantDenied(t, err, "not the owner", "outsider transfers to themselves")
	// With no app.user_id, through dbtx and as the migration user.
	err = f.transferAs(t, "", f.m1)
	wantDenied(t, err, "app.user_id is not set", "transfer with no user")
	_, err = f.pool.Exec(t.Context(), `SELECT transfer_ownership($1, $2)`, f.household, f.m1)
	wantDenied(t, err, "app.user_id is not set", "transfer outside dbtx")

	// As the owner, to someone who is not a member, or to themselves.
	wantSQLState(t, f.transferAs(t, f.owner, f.outsider), "P0002", "owner transfers to an outsider")
	wantSQLState(t, f.transferAs(t, f.owner, "0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"), "P0002", "owner transfers to no user")
	wantSQLState(t, f.transferAs(t, f.owner, f.owner), "22023", "owner transfers to themselves")
	// To a household that does not exist.
	err = inTxCommit(t, f.pool, f.owner, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `SELECT transfer_ownership('0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80', $1)`, f.m1)
		return err
	})
	wantDenied(t, err, "not the owner", "transfer of a missing household")

	if got := f.owners(t); !reflect.DeepEqual(got, []string{f.owner}) {
		t.Errorf("owners after rejected transfers = %v, want [%s]", got, f.owner)
	}
	if n := count(t, f.pool, `SELECT count(*) FROM household_members WHERE household_id = $1`, f.household); n != 3 {
		t.Errorf("%d members, want 3", n)
	}
}

// TestTransferOwnershipAppRoleCannotUpdate checks the owner cannot do the
// same thing with plain UPDATEs as shelfkin_app, which is why the function
// exists.
func TestTransferOwnershipAppRoleCannotUpdate(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	err := inTx(t, f.pool, f.owner, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(),
			`UPDATE household_members SET role = 'member' WHERE household_id = $1 AND user_id = $2`, f.household, f.owner)
		return err
	})
	wantDenied(t, err, permissionDenied, "owner demotes themselves with UPDATE")
}

// TestTransferOwnershipMigrationDownUp checks the migration's Down removes
// the function and Up restores it.
func TestTransferOwnershipMigrationDownUp(t *testing.T) {
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
		if filepath.Base(s.Path) == transferOwnershipMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", transferOwnershipMigration)
	}
	exists := func() int {
		return count(t, pool, `SELECT count(*) FROM pg_proc WHERE proname = 'transfer_ownership'`)
	}
	if n := exists(); n != 1 {
		t.Fatalf("before down: %d transfer_ownership functions, want 1", n)
	}
	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	if n := exists(); n != 0 {
		t.Fatalf("after down: %d transfer_ownership functions, want 0", n)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if n := exists(); n != 1 {
		t.Fatalf("after up: %d transfer_ownership functions, want 1", n)
	}
}
