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
	"github.com/marboleda/shelfkin/api/internal/dbtx"
)

// createHouseholdMigration adds the create_household function (#14).
const createHouseholdMigration = "20261104100000_create_household.sql"

// TestCreateHouseholdFunction checks create_household is SECURITY DEFINER
// with a fixed empty search_path, and that shelfkin_app can execute it but
// PUBLIC cannot.
func TestCreateHouseholdFunction(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	var (
		secdef, appExec, publicExec bool
		config                      []string
		result                      string
		grantees                    []string
	)
	err := pool.QueryRow(t.Context(), `
		SELECT p.prosecdef, p.proconfig, pg_get_function_result(p.oid),
		       has_function_privilege('shelfkin_app', p.oid, 'EXECUTE'),
		       EXISTS (SELECT 1 FROM aclexplode(p.proacl) a WHERE a.grantee = 0),
		       ARRAY(SELECT a.grantee::regrole::text || ':' || a.privilege_type
		             FROM aclexplode(p.proacl) a
		             WHERE a.grantee <> p.proowner ORDER BY 1)
		FROM pg_proc p
		WHERE p.proname = 'create_household' AND p.pronamespace = 'public'::regnamespace
		  AND pg_get_function_identity_arguments(p.oid) = 'name text'`,
	).Scan(&secdef, &config, &result, &appExec, &publicExec, &grantees)
	if err != nil {
		t.Fatalf("create_household(name text): %v", err)
	}
	if !secdef {
		t.Error("create_household is not SECURITY DEFINER")
	}
	if !reflect.DeepEqual(config, []string{`search_path=""`}) {
		t.Errorf("create_household config = %q, want [search_path=\"\"]", config)
	}
	if result != "uuid" {
		t.Errorf("create_household returns %s, want uuid", result)
	}
	if !appExec || publicExec {
		t.Errorf("shelfkin_app execute %v, PUBLIC execute %v; want true, false", appExec, publicExec)
	}
	if want := []string{"shelfkin_app:EXECUTE"}; !reflect.DeepEqual(grantees, want) {
		t.Errorf("grants on create_household = %q, want %q", grantees, want)
	}
}

// TestCreateHouseholdInsertsHouseholdAndOwner calls the function as
// shelfkin_app and checks the two rows it writes.
func TestCreateHouseholdInsertsHouseholdAndOwner(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "sam")

	var id string
	if err := inTx(t, pool, sam, func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(), `SELECT create_household('Home')::text`).Scan(&id); err != nil {
			return err
		}
		var name, createdBy, role, member string
		if err := tx.QueryRow(t.Context(), `
			SELECT h.name, h.created_by::text, m.role::text, m.user_id::text
			FROM households h JOIN household_members m ON m.household_id = h.id
			WHERE h.id = $1`, id).Scan(&name, &createdBy, &role, &member); err != nil {
			return err
		}
		if name != "Home" || createdBy != sam || role != "owner" || member != sam {
			t.Errorf("got name %q, created_by %s, role %s, member %s; want Home, %s, owner, %s",
				name, createdBy, role, member, sam, sam)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestCreateHouseholdNeedsUser checks the function fails, and writes nothing,
// when app.user_id is not set.
func TestCreateHouseholdNeedsUser(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	insertUser(t, pool, "sam")

	err := inTx(t, pool, "", func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `SELECT create_household('Home')`)
		return err
	})
	wantDenied(t, err, "app.user_id is not set", "create_household with no user")

	// As the migration user, with app.user_id never set in the session.
	_, err = pool.Exec(t.Context(), `SELECT create_household('Home')`)
	wantDenied(t, err, "app.user_id is not set", "create_household outside dbtx")

	if n := count(t, pool, `SELECT count(*) FROM households`); n != 0 {
		t.Errorf("%d households, want 0", n)
	}
}

// TestCreateHouseholdIsAtomic makes the owner row fail and checks that no
// household row is left behind.
func TestCreateHouseholdIsAtomic(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "sam")

	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION fail_member_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'owner row failed'; END $$;
		CREATE TRIGGER fail_member_insert BEFORE INSERT ON household_members
		FOR EACH ROW EXECUTE FUNCTION fail_member_insert();`); err != nil {
		t.Fatal(err)
	}

	// Commit for real (not inTx, which always rolls back) to show the failure
	// itself undoes the household insert.
	err := inTxCommit(t, pool, sam, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT create_household('Home')`)
		return err
	})
	if err == nil {
		t.Fatal("create_household succeeded with a failing owner row")
	}
	if n := count(t, pool, `SELECT count(*) FROM households`); n != 0 {
		t.Errorf("%d households left behind, want 0", n)
	}
}

// inTxCommit runs fn through dbtx.WithUser as user and commits if fn
// succeeds. It returns the transaction's error.
func inTxCommit(t *testing.T, pool *pgxpool.Pool, user string, fn func(pgx.Tx) error) error {
	t.Helper()
	return dbtx.WithUser(t.Context(), pool, pgUUID(t, user), fn)
}

// TestCreateHouseholdMigrationDownUp checks the migration's Down removes the
// function and Up restores it.
func TestCreateHouseholdMigrationDownUp(t *testing.T) {
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
		if filepath.Base(s.Path) == createHouseholdMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", createHouseholdMigration)
	}
	exists := func() int {
		return count(t, pool, `SELECT count(*) FROM pg_proc WHERE proname = 'create_household'`)
	}
	if n := exists(); n != 1 {
		t.Fatalf("before down: %d create_household functions, want 1", n)
	}
	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	if n := exists(); n != 0 {
		t.Fatalf("after down: %d create_household functions, want 0", n)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if n := exists(); n != 1 {
		t.Fatalf("after up: %d create_household functions, want 1", n)
	}
}
