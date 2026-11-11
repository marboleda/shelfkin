package db_test

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
)

// joinHouseholdMigration adds the join_household function (#16).
const joinHouseholdMigration = "20261111100000_join_household.sql"

// joinFixture is a household owned by owner with a member, an active invite
// code, and an outsider who is in no household.
type joinFixture struct {
	pool                    *pgxpool.Pool
	household               string
	owner, member, outsider string
	code                    string
}

func newJoinFixture(t *testing.T) *joinFixture {
	t.Helper()
	pool := dbtest.New(t)
	f := &joinFixture{
		pool:     pool,
		owner:    insertUser(t, pool, "owner"),
		member:   insertUser(t, pool, "member"),
		outsider: insertUser(t, pool, "outsider"),
		code:     "ABCDEFGH",
	}
	f.household = f.newHousehold(t, f.owner)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'member')`,
		f.household, f.member); err != nil {
		t.Fatal(err)
	}
	f.addInvite(t, f.household, f.code, "7 days", false)
	return f
}

// newHousehold inserts a household owned by owner and returns its ID.
func (f *joinFixture) newHousehold(t *testing.T, owner string) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(t.Context(),
		`INSERT INTO households (name, created_by) VALUES ('Home', $1) RETURNING id::text`, owner,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(),
		`INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'owner')`, id, owner); err != nil {
		t.Fatal(err)
	}
	return id
}

// addInvite inserts an invite expiring the given interval from now (negative
// for already expired), revoked if revoked is true.
func (f *joinFixture) addInvite(t *testing.T, household, code, expiresIn string, revoked bool) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `
		INSERT INTO household_invites (household_id, code, expires_at, revoked_at)
		VALUES ($1, $2, now() + $3::interval, CASE WHEN $4 THEN now() END)`,
		household, code, expiresIn, revoked); err != nil {
		t.Fatal(err)
	}
}

// joinAs calls join_household as user through dbtx and commits if it
// succeeds. It returns the household ID ("" for NULL) and status.
func (f *joinFixture) joinAs(t *testing.T, user, code string) (string, string, error) {
	t.Helper()
	var household *string
	var status string
	err := inTxCommit(t, f.pool, user, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `SELECT household_id::text, status FROM join_household($1)`, code).
			Scan(&household, &status)
	})
	if household == nil {
		return "", status, err
	}
	return *household, status, err
}

func (f *joinFixture) wantJoin(t *testing.T, user, code, wantHousehold, wantStatus string) {
	t.Helper()
	household, status, err := f.joinAs(t, user, code)
	if err != nil {
		t.Fatalf("join_household(%q) as %s: %v", code, user, err)
	}
	if household != wantHousehold || status != wantStatus {
		t.Errorf("join_household(%q) = (%q, %q), want (%q, %q)", code, household, status, wantHousehold, wantStatus)
	}
}

func (f *joinFixture) role(t *testing.T, user string) string {
	t.Helper()
	var role string
	err := f.pool.QueryRow(t.Context(),
		`SELECT role::text FROM household_members WHERE household_id = $1 AND user_id = $2`, f.household, user).Scan(&role)
	if err == pgx.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return role
}

// TestJoinHouseholdFunction checks join_household is SECURITY DEFINER with a
// fixed empty search_path, and that shelfkin_app can execute it but PUBLIC
// cannot.
func TestJoinHouseholdFunction(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	var (
		secdef, appExec, publicExec bool
		volatile, result            string
		config, grantees            []string
	)
	err := pool.QueryRow(t.Context(), `
		SELECT p.prosecdef, p.provolatile::text, p.proconfig, pg_get_function_result(p.oid),
		       has_function_privilege('shelfkin_app', 'join_household(text)', 'EXECUTE'),
		       EXISTS (SELECT 1 FROM aclexplode(p.proacl) a WHERE a.grantee = 0),
		       ARRAY(SELECT a.grantee::regrole::text || ':' || a.privilege_type
		             FROM aclexplode(p.proacl) a
		             WHERE a.grantee <> p.proowner ORDER BY 1)
		FROM pg_proc p
		WHERE p.oid = 'public.join_household(text)'::regprocedure`,
	).Scan(&secdef, &volatile, &config, &result, &appExec, &publicExec, &grantees)
	if err != nil {
		t.Fatalf("join_household(text): %v", err)
	}
	if !secdef {
		t.Error("join_household is not SECURITY DEFINER")
	}
	if volatile != "v" {
		t.Errorf("join_household volatility = %q, want v (VOLATILE)", volatile)
	}
	if !reflect.DeepEqual(config, []string{`search_path=""`}) {
		t.Errorf("join_household config = %q, want [search_path=\"\"]", config)
	}
	if result != "record" {
		t.Errorf("join_household returns %s, want record (household_id, status)", result)
	}
	if !appExec || publicExec {
		t.Errorf("shelfkin_app execute %v, PUBLIC execute %v; want true, false", appExec, publicExec)
	}
	if want := []string{"shelfkin_app:EXECUTE"}; !reflect.DeepEqual(grantees, want) {
		t.Errorf("grants on join_household = %q, want %q", grantees, want)
	}
}

// TestJoinHouseholdNeedsUser checks join_household fails with 42501 when
// app.user_id is not set, through dbtx and as the migration user.
func TestJoinHouseholdNeedsUser(t *testing.T) {
	t.Parallel()
	f := newJoinFixture(t)

	err := inTx(t, f.pool, "", func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `SELECT * FROM join_household($1)`, f.code)
		return err
	})
	wantDenied(t, err, "app.user_id is not set", "join with no user")
	_, err = f.pool.Exec(t.Context(), `SELECT * FROM join_household($1)`, f.code)
	wantDenied(t, err, "app.user_id is not set", "join outside dbtx")
	if n := count(t, f.pool, `SELECT count(*) FROM household_members WHERE household_id = $1`, f.household); n != 2 {
		t.Errorf("%d members, want 2", n)
	}
}

// TestJoinHouseholdStatuses checks every status join_household returns, and
// that only joined adds a row.
func TestJoinHouseholdStatuses(t *testing.T) {
	t.Parallel()
	f := newJoinFixture(t)
	f.addInvite(t, f.household, "EXPRDCDE", "-1 second", false)
	f.addInvite(t, f.household, "RVKDCDE2", "7 days", true)
	f.addInvite(t, f.household, "BTHCDE23", "-1 day", true)

	// Unknown, revoked, expired, and revoked takes precedence over expired.
	// The household ID is NULL for each.
	f.wantJoin(t, f.outsider, "ZZZZZZZZ", "", "not_found")
	f.wantJoin(t, f.outsider, "abcdefgh", "", "not_found") // matched exactly
	f.wantJoin(t, f.outsider, "RVKDCDE2", "", "revoked")
	f.wantJoin(t, f.outsider, "EXPRDCDE", "", "expired")
	f.wantJoin(t, f.outsider, "BTHCDE23", "", "revoked")
	if r := f.role(t, f.outsider); r != "" {
		t.Fatalf("outsider has role %q after failed joins, want none", r)
	}

	// Members and the owner are already members, even with an expired or
	// revoked code; nothing changes.
	for _, code := range []string{f.code, "EXPRDCDE", "RVKDCDE2", "BTHCDE23"} {
		f.wantJoin(t, f.member, code, f.household, "already_member")
		f.wantJoin(t, f.owner, code, f.household, "already_member")
	}
	if f.role(t, f.owner) != "owner" || f.role(t, f.member) != "member" {
		t.Errorf("roles changed: owner %q, member %q", f.role(t, f.owner), f.role(t, f.member))
	}

	// A valid code adds the caller as a member; a second call is a no-op.
	f.wantJoin(t, f.outsider, f.code, f.household, "joined")
	if r := f.role(t, f.outsider); r != "member" {
		t.Errorf("outsider's role after joining = %q, want member", r)
	}
	f.wantJoin(t, f.outsider, f.code, f.household, "already_member")
	if n := count(t, f.pool, `SELECT count(*) FROM household_members WHERE household_id = $1`, f.household); n != 3 {
		t.Errorf("%d members, want 3", n)
	}
	// The invite is unchanged.
	if n := count(t, f.pool, `SELECT count(*) FROM household_invites WHERE code = $1 AND revoked_at IS NULL AND expires_at > now()`, f.code); n != 1 {
		t.Error("the invite changed after being used")
	}
}

// TestJoinHouseholdOtherHousehold checks a code joins only its own household.
func TestJoinHouseholdOtherHousehold(t *testing.T) {
	t.Parallel()
	f := newJoinFixture(t)
	otherOwner := insertUser(t, f.pool, "other")
	other := f.newHousehold(t, otherOwner)
	f.addInvite(t, other, "QQQQQQQQ", "7 days", false)

	// The member of Home joins Other; Home is untouched.
	f.wantJoin(t, f.member, "QQQQQQQQ", other, "joined")
	if n := count(t, f.pool, `SELECT count(*) FROM household_members WHERE household_id = $1`, f.household); n != 2 {
		t.Errorf("Home has %d members, want 2", n)
	}
	// Other's owner using Home's code joins Home, not Other.
	f.wantJoin(t, otherOwner, f.code, f.household, "joined")
	if r := f.role(t, otherOwner); r != "member" {
		t.Errorf("other owner's role in Home = %q, want member", r)
	}
}

// TestJoinHouseholdRLSUnchanged checks a member who joined still cannot read
// the household's invites as shelfkin_app: the owner-only policy is
// unchanged and the function is the only way in.
func TestJoinHouseholdRLSUnchanged(t *testing.T) {
	t.Parallel()
	f := newJoinFixture(t)
	f.wantJoin(t, f.outsider, f.code, f.household, "joined")

	for _, user := range []string{f.outsider, f.member} {
		if n := countAs(t, f.pool, user, `SELECT count(*) FROM household_invites`); n != 0 {
			t.Errorf("member %s sees %d invites, want 0", user, n)
		}
	}
	if n := countAs(t, f.pool, f.owner, `SELECT count(*) FROM household_invites`); n != 1 {
		t.Errorf("owner sees %d invites, want 1", n)
	}
}

// TestJoinHouseholdWaitsForDeletion checks a join that waits on a household
// deletion in another transaction returns not_found once it commits.
func TestJoinHouseholdWaitsForDeletion(t *testing.T) {
	t.Parallel()
	f := newJoinFixture(t)
	ctx := t.Context()

	del, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer del.Rollback(ctx)
	if _, err := del.Exec(ctx, `DELETE FROM households WHERE id = $1`, f.household); err != nil {
		t.Fatal(err)
	}

	type result struct {
		household, status string
		err               error
	}
	done := make(chan result, 1)
	go func() {
		var household *string
		var status string
		err := inTxCommit(t, f.pool, f.outsider, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT household_id::text, status FROM join_household($1)`, f.code).
				Scan(&household, &status)
		})
		r := result{status: status, err: err}
		if household != nil {
			r.household = *household
		}
		done <- r
	}()

	// Wait until the join is blocked on a lock held by the deletion.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := f.pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			  AND query LIKE '%join_household%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("join finished before the deletion committed: %+v", r)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the join never waited on the deletion")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := del.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("join after the deletion: %v", r.err)
	}
	if r.household != "" || r.status != "not_found" {
		t.Errorf("join after the deletion = (%q, %q), want (\"\", not_found)", r.household, r.status)
	}
	if n := count(t, f.pool, `SELECT count(*) FROM household_members WHERE user_id = $1`, f.outsider); n != 0 {
		t.Errorf("outsider has %d memberships, want 0", n)
	}
}

// TestJoinHouseholdConcurrentSameUser checks two simultaneous joins by one
// user add one row: one joined, the other already_member.
func TestJoinHouseholdConcurrentSameUser(t *testing.T) {
	t.Parallel()
	f := newJoinFixture(t)
	ctx := t.Context()

	// The first join inserts but does not commit yet.
	first, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	if _, err := first.Exec(ctx, `SET LOCAL ROLE shelfkin_app`); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, f.outsider); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := first.QueryRow(ctx, `SELECT status FROM join_household($1)`, f.code).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "joined" {
		t.Fatalf("first join = %q, want joined", status)
	}

	done := make(chan string, 1)
	go func() {
		_, s, err := f.joinAs(t, f.outsider, f.code)
		if err != nil {
			s = "error: " + err.Error()
		}
		done <- s
	}()
	// Give the second join time to block on the first's uncommitted row.
	select {
	case s := <-done:
		t.Fatalf("second join finished before the first committed: %s", s)
	case <-time.After(300 * time.Millisecond):
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if s := <-done; s != "already_member" {
		t.Errorf("second join = %q, want already_member", s)
	}
	if n := count(t, f.pool, `SELECT count(*) FROM household_members WHERE user_id = $1`, f.outsider); n != 1 {
		t.Errorf("outsider has %d memberships, want 1", n)
	}
}

// TestJoinHouseholdMigrationDownUp checks the migration's Down removes the
// function and Up restores it.
func TestJoinHouseholdMigrationDownUp(t *testing.T) {
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
		if filepath.Base(s.Path) == joinHouseholdMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", joinHouseholdMigration)
	}
	exists := func() int {
		return count(t, pool, `SELECT count(*) FROM pg_proc WHERE proname = 'join_household'`)
	}
	if n := exists(); n != 1 {
		t.Fatalf("before down: %d join_household functions, want 1", n)
	}
	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	if n := exists(); n != 0 {
		t.Fatalf("after down: %d join_household functions, want 0", n)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if n := exists(); n != 1 {
		t.Fatalf("after up: %d join_household functions, want 1", n)
	}
}
