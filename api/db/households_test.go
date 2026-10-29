package db_test

import (
	"context"
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

// householdsMigration is the file that creates households, household_members,
// household_invites and the member_role type.
const householdsMigration = "20261029120000_households.sql"

func TestMemberRoleEnum(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	rows, err := pool.Query(t.Context(), `SELECT unnest(enum_range(NULL::member_role))::text`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"owner", "member"}; !reflect.DeepEqual(got, want) {
		t.Errorf("member_role values = %v, want %v", got, want)
	}
}

func TestHouseholdTablesColumns(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	tests := map[string][]column{
		"households": {
			{"id", "uuid", false, "uuidv7()"},
			{"name", "text", false, ""},
			{"created_by", "uuid", true, ""},
			{"created_at", "timestamp with time zone", false, "now()"},
			{"updated_at", "timestamp with time zone", false, "now()"},
		},
		"household_members": {
			{"household_id", "uuid", false, ""},
			{"user_id", "uuid", false, ""},
			{"role", "member_role", false, ""},
			{"joined_at", "timestamp with time zone", false, "now()"},
		},
		"household_invites": {
			{"id", "uuid", false, "uuidv7()"},
			{"household_id", "uuid", false, ""},
			{"code", "text", false, ""},
			{"created_by", "uuid", true, ""},
			{"created_at", "timestamp with time zone", false, "now()"},
			{"expires_at", "timestamp with time zone", false, ""},
			{"revoked_at", "timestamp with time zone", true, ""},
		},
	}
	for table, want := range tests {
		if got := tableColumns(t, pool, table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s columns:\n got  %+v\n want %+v", table, got, want)
		}
	}
}

func TestHouseholdTablesConstraints(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	rows, err := pool.Query(t.Context(), `
		SELECT conrelid::regclass::text, pg_get_constraintdef(oid), condeferrable
		FROM pg_constraint
		WHERE conrelid IN ('households'::regclass, 'household_members'::regclass, 'household_invites'::regclass)
		  AND contype <> 'n' -- NOT NULL (Postgres 18+) is checked by the columns test
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	type con struct {
		Table, Def string
		Deferrable bool
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (con, error) {
		var c con
		err := r.Scan(&c.Table, &c.Def, &c.Deferrable)
		return c, err
	})
	if err != nil {
		t.Fatal(err)
	}
	// The created_by foreign keys have no ON DELETE action (#46 changes them
	// to SET NULL). There are no CHECK constraints.
	want := []con{
		{"household_invites", "FOREIGN KEY (created_by) REFERENCES users(id)", false},
		{"household_invites", "FOREIGN KEY (household_id) REFERENCES households(id) ON DELETE CASCADE", false},
		{"household_invites", "PRIMARY KEY (id)", false},
		{"household_invites", "UNIQUE (code)", false},
		{"household_members", "FOREIGN KEY (household_id) REFERENCES households(id) ON DELETE CASCADE", false},
		{"household_members", "FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE", false},
		{"household_members", "PRIMARY KEY (household_id, user_id)", false},
		{"households", "FOREIGN KEY (created_by) REFERENCES users(id)", false},
		{"households", "PRIMARY KEY (id)", false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("constraints:\n got  %+v\n want %+v", got, want)
	}
}

func TestHouseholdMembersIndexes(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	// Every index except the one backing the primary key.
	rows, err := pool.Query(t.Context(), `
		SELECT pg_get_indexdef(indexrelid)
		FROM pg_index
		WHERE indrelid = 'household_members'::regclass AND NOT indisprimary
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CREATE INDEX household_members_user_id ON public.household_members USING btree (user_id)",
		"CREATE UNIQUE INDEX household_members_one_owner ON public.household_members USING btree (household_id) WHERE (role = 'owner'::member_role)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("household_members indexes:\n got  %q\n want %q", got, want)
	}
}

// execer is satisfied by *pgxpool.Pool and pgx.Tx.
type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func insertHousehold(t *testing.T, pool *pgxpool.Pool, name string, createdBy any) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO households (name, created_by) VALUES ($1, $2) RETURNING id`, name, createdBy).Scan(&id)
	if err != nil {
		t.Fatalf("inserting household %s: %v", name, err)
	}
	return id
}

func insertMember(ctx context.Context, q execer, householdID, userID, role string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, $3)`,
		householdID, userID, role)
	return err
}

func setRole(ctx context.Context, q execer, householdID, userID, role string) error {
	_, err := q.Exec(ctx,
		`UPDATE household_members SET role = $3 WHERE household_id = $1 AND user_id = $2`,
		householdID, userID, role)
	return err
}

func insertInvite(ctx context.Context, q execer, householdID, code string, createdBy any) error {
	_, err := q.Exec(ctx,
		`INSERT INTO household_invites (household_id, code, created_by, expires_at)
		 VALUES ($1, $2, $3, now() + interval '7 days')`,
		householdID, code, createdBy)
	return err
}

func mustInsertMember(t *testing.T, pool *pgxpool.Pool, householdID, userID, role string) {
	t.Helper()
	if err := insertMember(t.Context(), pool, householdID, userID, role); err != nil {
		t.Fatalf("adding %s as %s: %v", userID, role, err)
	}
}

// owners returns the user IDs with role owner in a household.
func owners(t *testing.T, pool *pgxpool.Pool, householdID string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT user_id::text FROM household_members WHERE household_id = $1 AND role = 'owner' ORDER BY 1`,
		householdID)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func TestHouseholdDefaults(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	before := time.Now().Add(-time.Minute)

	var (
		id                   string
		version              int
		createdAt, updatedAt time.Time
	)
	err := pool.QueryRow(ctx, `
		INSERT INTO households (name, created_by) VALUES ('Home', $1)
		RETURNING id, uuid_extract_version(id), created_at, updated_at`, sam,
	).Scan(&id, &version, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("inserting household: %v", err)
	}
	if version != 7 {
		t.Errorf("households.id version = %d, want 7", version)
	}
	if createdAt.Before(before) || updatedAt.Before(before) {
		t.Errorf("households timestamps not defaulted to now: created_at %v, updated_at %v", createdAt, updatedAt)
	}

	var joinedAt time.Time
	err = pool.QueryRow(ctx, `
		INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'owner')
		RETURNING joined_at`, id, sam).Scan(&joinedAt)
	if err != nil {
		t.Fatalf("inserting member: %v", err)
	}
	if joinedAt.Before(before) {
		t.Errorf("household_members.joined_at = %v, want now", joinedAt)
	}

	var (
		inviteVersion int
		inviteCreated time.Time
		revokedAt     *time.Time
	)
	err = pool.QueryRow(ctx, `
		INSERT INTO household_invites (household_id, code, created_by, expires_at)
		VALUES ($1, 'ABCD2345', $2, now() + interval '7 days')
		RETURNING uuid_extract_version(id), created_at, revoked_at`, id, sam,
	).Scan(&inviteVersion, &inviteCreated, &revokedAt)
	if err != nil {
		t.Fatalf("inserting invite: %v", err)
	}
	if inviteVersion != 7 {
		t.Errorf("household_invites.id version = %d, want 7", inviteVersion)
	}
	if inviteCreated.Before(before) {
		t.Errorf("household_invites.created_at = %v, want now", inviteCreated)
	}
	if revokedAt != nil {
		t.Errorf("household_invites.revoked_at = %v, want NULL", *revokedAt)
	}

	// created_by is nullable on both tables.
	other := insertHousehold(t, pool, "No creator", nil)
	if err := insertInvite(ctx, pool, other, "NOCREATR", nil); err != nil {
		t.Errorf("inserting invite without created_by: %v", err)
	}
}

func TestHouseholdRequiredColumns(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	home := insertHousehold(t, pool, "Home", sam)

	_, err := pool.Exec(ctx, `INSERT INTO households (created_by) VALUES ($1)`, sam)
	wantSQLState(t, err, sqlStateNotNull, "household without name")
	_, err = pool.Exec(ctx, `INSERT INTO household_members (household_id, user_id) VALUES ($1, $2)`, home, sam)
	wantSQLState(t, err, sqlStateNotNull, "member without role")
	_, err = pool.Exec(ctx, `INSERT INTO household_invites (household_id, code) VALUES ($1, 'ABCD2345')`, home)
	wantSQLState(t, err, sqlStateNotNull, "invite without expires_at")
	_, err = pool.Exec(ctx,
		`INSERT INTO household_invites (household_id, expires_at) VALUES ($1, now())`, home)
	wantSQLState(t, err, sqlStateNotNull, "invite without code")
	_, err = pool.Exec(ctx,
		`INSERT INTO household_invites (code, expires_at) VALUES ('ABCD2345', now())`)
	wantSQLState(t, err, sqlStateNotNull, "invite without household_id")
}

func TestHouseholdMemberRoleRejectsUnknownValue(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	home := insertHousehold(t, pool, "Home", sam)

	err := insertMember(t.Context(), pool, home, sam, "admin")
	wantSQLState(t, err, "22P02", "member with role admin") // invalid_text_representation
}

func TestHouseholdOneOwnerAndMembers(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	kim := insertUser(t, pool, "fb-kim")
	home := insertHousehold(t, pool, "Home", sam)

	mustInsertMember(t, pool, home, sam, "owner")
	mustInsertMember(t, pool, home, alex, "member")
	mustInsertMember(t, pool, home, kim, "member")

	if n := count(t, pool, `SELECT count(*) FROM household_members WHERE household_id = $1`, home); n != 3 {
		t.Errorf("household has %d members, want 3", n)
	}
	if got := owners(t, pool, home); !reflect.DeepEqual(got, []string{sam}) {
		t.Errorf("owners = %v, want [%s]", got, sam)
	}
}

func TestHouseholdSecondOwnerRejected(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	kim := insertUser(t, pool, "fb-kim")
	home := insertHousehold(t, pool, "Home", sam)
	mustInsertMember(t, pool, home, sam, "owner")
	mustInsertMember(t, pool, home, kim, "member")

	err := insertMember(t.Context(), pool, home, alex, "owner")
	wantSQLState(t, err, sqlStateUnique, "inserting second owner")
	err = setRole(t.Context(), pool, home, kim, "owner")
	wantSQLState(t, err, sqlStateUnique, "promoting member to second owner")
}

func TestHouseholdOwnersAcrossHouseholds(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	home := insertHousehold(t, pool, "Home", sam)
	cabin := insertHousehold(t, pool, "Cabin", alex)

	mustInsertMember(t, pool, home, sam, "owner")
	mustInsertMember(t, pool, cabin, alex, "owner")
	// Sam owns Home and is a member of Cabin.
	mustInsertMember(t, pool, cabin, sam, "member")

	if got := owners(t, pool, home); !reflect.DeepEqual(got, []string{sam}) {
		t.Errorf("Home owners = %v, want [%s]", got, sam)
	}
	if got := owners(t, pool, cabin); !reflect.DeepEqual(got, []string{alex}) {
		t.Errorf("Cabin owners = %v, want [%s]", got, alex)
	}
}

func TestHouseholdMemberTwiceRejected(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	home := insertHousehold(t, pool, "Home", sam)
	mustInsertMember(t, pool, home, sam, "owner")
	mustInsertMember(t, pool, home, alex, "member")

	err := insertMember(t.Context(), pool, home, alex, "member")
	wantSQLState(t, err, sqlStateUnique, "adding member twice")
}

func TestHouseholdOwnershipTransferDemoteThenPromote(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	home := insertHousehold(t, pool, "Home", sam)
	mustInsertMember(t, pool, home, sam, "owner")
	mustInsertMember(t, pool, home, alex, "member")

	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := setRole(ctx, tx, home, sam, "member"); err != nil {
			return err
		}
		return setRole(ctx, tx, home, alex, "owner")
	})
	if err != nil {
		t.Fatalf("transfer (demote, then promote): %v", err)
	}
	if got := owners(t, pool, home); !reflect.DeepEqual(got, []string{alex}) {
		t.Errorf("owners after transfer = %v, want [%s]", got, alex)
	}
}

func TestHouseholdOwnershipTransferPromoteFirstFails(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	home := insertHousehold(t, pool, "Home", sam)
	mustInsertMember(t, pool, home, sam, "owner")
	mustInsertMember(t, pool, home, alex, "member")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	promoteErr := setRole(ctx, tx, home, alex, "owner")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	wantSQLState(t, promoteErr, sqlStateUnique, "promoting before demoting")

	if got := owners(t, pool, home); !reflect.DeepEqual(got, []string{sam}) {
		t.Errorf("owners after rollback = %v, want [%s]", got, sam)
	}
}

func TestDeletingHouseholdCascades(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	home := insertHousehold(t, pool, "Home", sam)
	cabin := insertHousehold(t, pool, "Cabin", alex)
	mustInsertMember(t, pool, home, sam, "owner")
	mustInsertMember(t, pool, home, alex, "member")
	mustInsertMember(t, pool, cabin, alex, "owner")
	mustInsertMember(t, pool, cabin, sam, "member")
	for _, inv := range []struct{ household, code, by string }{
		{home, "HOME0001", sam},
		{home, "HOME0002", sam},
		{cabin, "CABIN001", alex},
	} {
		if err := insertInvite(ctx, pool, inv.household, inv.code, inv.by); err != nil {
			t.Fatalf("inserting invite %s: %v", inv.code, err)
		}
	}

	if _, err := pool.Exec(ctx, `DELETE FROM households WHERE id = $1`, home); err != nil {
		t.Fatalf("deleting household: %v", err)
	}

	if n := count(t, pool, `SELECT count(*) FROM household_members WHERE household_id = $1`, home); n != 0 {
		t.Errorf("deleted household still has %d members", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM household_invites WHERE household_id = $1`, home); n != 0 {
		t.Errorf("deleted household still has %d invites", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM household_members WHERE household_id = $1`, cabin); n != 2 {
		t.Errorf("other household has %d members, want 2", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM household_invites WHERE household_id = $1`, cabin); n != 1 {
		t.Errorf("other household has %d invites, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM users`); n != 2 {
		t.Errorf("%d users after deleting household, want 2", n)
	}
}

func TestDeletingUserRemovesMemberships(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	home := insertHousehold(t, pool, "Home", sam)
	cabin := insertHousehold(t, pool, "Cabin", sam)
	mustInsertMember(t, pool, home, sam, "owner")
	mustInsertMember(t, pool, home, alex, "member")
	mustInsertMember(t, pool, cabin, sam, "owner")
	mustInsertMember(t, pool, cabin, alex, "member")

	// Alex created no household or invite, so deleting Alex is allowed.
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, alex); err != nil {
		t.Fatalf("deleting user: %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM household_members WHERE user_id = $1`, alex); n != 0 {
		t.Errorf("deleted user still has %d memberships", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM household_members WHERE user_id = $1`, sam); n != 2 {
		t.Errorf("other user has %d memberships, want 2", n)
	}
}

func TestDeletingCreatorUserFails(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	// Sam created a household.
	sam := insertUser(t, pool, "fb-sam")
	insertHousehold(t, pool, "Home", sam)
	_, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, sam)
	wantSQLState(t, err, sqlStateForeignKey, "deleting user who created a household")

	// Alex created only an invite, in a household Alex did not create.
	alex := insertUser(t, pool, "fb-alex")
	cabin := insertHousehold(t, pool, "Cabin", nil)
	if err := insertInvite(ctx, pool, cabin, "CABIN001", alex); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, alex)
	wantSQLState(t, err, sqlStateForeignKey, "deleting user who created an invite")
}

func TestHouseholdUnknownReferences(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	home := insertHousehold(t, pool, "Home", sam)
	const missing = "0199a000-0000-7000-8000-000000000000"

	_, err := pool.Exec(ctx, `INSERT INTO households (name, created_by) VALUES ('X', $1)`, missing)
	wantSQLState(t, err, sqlStateForeignKey, "household created by missing user")
	wantSQLState(t, insertMember(ctx, pool, missing, sam, "member"), sqlStateForeignKey, "member of missing household")
	wantSQLState(t, insertMember(ctx, pool, home, missing, "member"), sqlStateForeignKey, "missing user as member")
	wantSQLState(t, insertInvite(ctx, pool, missing, "MISSING1", sam), sqlStateForeignKey, "invite for missing household")
	wantSQLState(t, insertInvite(ctx, pool, home, "MISSING2", missing), sqlStateForeignKey, "invite by missing user")
}

func TestHouseholdInviteCodeUnique(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	home := insertHousehold(t, pool, "Home", sam)
	cabin := insertHousehold(t, pool, "Cabin", sam)

	if err := insertInvite(ctx, pool, home, "ABCD2345", sam); err != nil {
		t.Fatal(err)
	}
	wantSQLState(t, insertInvite(ctx, pool, home, "ABCD2345", sam), sqlStateUnique, "same code, same household")
	wantSQLState(t, insertInvite(ctx, pool, cabin, "ABCD2345", sam), sqlStateUnique, "same code, different household")
	// A household can have several invites with different codes.
	if err := insertInvite(ctx, pool, home, "WXYZ6789", sam); err != nil {
		t.Errorf("second invite with a new code: %v", err)
	}
}

// TestHouseholdsMigrationDownUp rolls back to just before this migration
// (which also reverts any later ones), checks the tables and type are gone,
// then migrates up again.
func TestHouseholdsMigrationDownUp(t *testing.T) {
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
		if filepath.Base(s.Path) == householdsMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", householdsMigration)
	}

	type objects struct{ Households, Members, Invites, Role bool }
	exist := func() objects {
		t.Helper()
		var o objects
		if err := pool.QueryRow(ctx, `
			SELECT to_regclass('households') IS NOT NULL,
			       to_regclass('household_members') IS NOT NULL,
			       to_regclass('household_invites') IS NOT NULL,
			       to_regtype('member_role') IS NOT NULL`,
		).Scan(&o.Households, &o.Members, &o.Invites, &o.Role); err != nil {
			t.Fatal(err)
		}
		return o
	}

	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	if got := exist(); got != (objects{}) {
		t.Fatalf("after down: %+v; want all dropped", got)
	}
	if _, err := provider.UpTo(ctx, this); err != nil {
		t.Fatalf("up to %d: %v", this, err)
	}
	if got, want := exist(), (objects{true, true, true, true}); got != want {
		t.Fatalf("after up: %+v; want all created", got)
	}
	if got := tableColumns(t, pool, "household_invites"); len(got) != 7 {
		t.Errorf("household_invites after re-up has %d columns, want 7", len(got))
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
}
