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

// entriesMigration is the file that creates pantry_entries, item_events,
// expiry_notifications and the stock_level and event_type types.
const entriesMigration = "20261102230000_pantry_entries_events.sql"

// sqlStateInvalidText is raised for a value that is not in an enum.
const sqlStateInvalidText = "22P02"

// newID returns a fresh UUIDv7, standing in for a client-generated ID.
func newID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `SELECT uuidv7()::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// insertEntry adds a pantry entry with level 'full' in the fridge, created and
// updated by the given user (which may be nil), and returns its ID.
func insertEntry(t *testing.T, pool *pgxpool.Pool, householdID, itemID string, by any) string {
	t.Helper()
	id := newID(t, pool)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level, created_by, updated_by)
		VALUES ($1, $2, $3, $4, 'full', $5, $5)`, id, householdID, itemID, locFridge, by)
	if err != nil {
		t.Fatalf("inserting entry: %v", err)
	}
	return id
}

// insertEvent adds an event of the given type and returns its ID.
func insertEvent(t *testing.T, pool *pgxpool.Pool, householdID, entryID, itemID string, actor any, typ string) string {
	t.Helper()
	id := newID(t, pool)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO item_events (id, household_id, entry_id, catalog_item_id, actor_id, type, snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, '{"name": "Milk", "level": "full"}')`,
		id, householdID, entryID, itemID, actor, typ)
	if err != nil {
		t.Fatalf("inserting %s event: %v", typ, err)
	}
	return id
}

func enumValues(t *testing.T, pool *pgxpool.Pool, typ string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT enumlabel::text FROM pg_enum WHERE enumtypid = to_regtype($1) ORDER BY enumsortorder`, typ)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestEntryEnums(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	tests := map[string][]string{
		"stock_level": {"full", "half", "low", "out"},
		"event_type": {
			"entry_created", "level_changed", "expiry_changed",
			"location_changed", "purchase_date_changed", "entry_deleted",
		},
	}
	for typ, want := range tests {
		if got := enumValues(t, pool, typ); !reflect.DeepEqual(got, want) {
			t.Errorf("%s values = %v, want %v", typ, got, want)
		}
	}
}

func TestEntryTablesColumns(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	const ts = "timestamp with time zone"
	tests := map[string][]column{
		"pantry_entries": {
			{"id", "uuid", false, ""},
			{"household_id", "uuid", false, ""},
			{"catalog_item_id", "uuid", false, ""},
			{"location_id", "smallint", false, ""},
			{"level", "stock_level", false, ""},
			{"purchased_on", "date", false, "CURRENT_DATE"},
			{"expires_on", "date", true, ""},
			{"created_by", "uuid", true, ""},
			{"updated_by", "uuid", true, ""},
			{"created_at", ts, false, "now()"},
			{"updated_at", ts, false, "now()"},
		},
		"item_events": {
			{"id", "uuid", false, ""},
			{"household_id", "uuid", false, ""},
			{"entry_id", "uuid", false, ""},
			{"catalog_item_id", "uuid", false, ""},
			{"actor_id", "uuid", true, ""},
			{"type", "event_type", false, ""},
			{"changes", "jsonb", false, "'{}'::jsonb"},
			{"snapshot", "jsonb", false, ""},
			{"occurred_at", ts, false, "now()"},
		},
		"expiry_notifications": {
			{"entry_id", "uuid", false, ""},
			{"user_id", "uuid", false, ""},
			{"expires_on", "date", false, ""},
			{"sent_at", ts, false, "now()"},
		},
	}
	for table, want := range tests {
		if got := tableColumns(t, pool, table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s columns:\n got  %+v\n want %+v", table, got, want)
		}
	}
}

// TestEntryTablesConstraints also checks that item_events.entry_id,
// item_events.catalog_item_id and expiry_notifications.entry_id have no
// foreign keys, and that there are no CHECK or UNIQUE constraints.
func TestEntryTablesConstraints(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	rows, err := pool.Query(t.Context(), `
		SELECT conrelid::regclass::text, pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid IN ('pantry_entries'::regclass, 'item_events'::regclass, 'expiry_notifications'::regclass)
		  AND contype <> 'n' -- NOT NULL is checked by the columns test
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
	want := [][2]string{
		{"expiry_notifications", "FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE"},
		{"expiry_notifications", "PRIMARY KEY (entry_id, user_id, expires_on)"},
		{"item_events", "FOREIGN KEY (actor_id) REFERENCES users(id) ON DELETE SET NULL"},
		{"item_events", "FOREIGN KEY (household_id) REFERENCES households(id) ON DELETE CASCADE"},
		{"item_events", "PRIMARY KEY (id)"},
		{"pantry_entries", "FOREIGN KEY (catalog_item_id) REFERENCES catalog_items(id)"},
		{"pantry_entries", "FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL"},
		{"pantry_entries", "FOREIGN KEY (household_id) REFERENCES households(id) ON DELETE CASCADE"},
		{"pantry_entries", "FOREIGN KEY (location_id) REFERENCES locations(id)"},
		{"pantry_entries", "FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL"},
		{"pantry_entries", "PRIMARY KEY (id)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("constraints:\n got  %v\n want %v", got, want)
	}
}

func TestEntryTablesIndexes(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	rows, err := pool.Query(t.Context(), `
		SELECT pg_get_indexdef(indexrelid)
		FROM pg_index
		WHERE indrelid IN ('pantry_entries'::regclass, 'item_events'::regclass, 'expiry_notifications'::regclass)
		  AND NOT indisprimary
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CREATE INDEX item_events_entry_id_occurred_at_idx ON public.item_events USING btree (entry_id, occurred_at DESC)",
		"CREATE INDEX item_events_household_id_occurred_at_idx ON public.item_events USING btree (household_id, occurred_at DESC)",
		"CREATE INDEX pantry_entries_household_id_catalog_item_id_idx ON public.pantry_entries USING btree (household_id, catalog_item_id)",
		"CREATE INDEX pantry_entries_household_id_expires_on_idx ON public.pantry_entries USING btree (household_id, expires_on) WHERE (expires_on IS NOT NULL)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("indexes:\n got  %q\n want %q", got, want)
	}
}

func TestEntryAndEventDefaults(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	home := insertHousehold(t, pool, "Home", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)
	before := time.Now().Add(-time.Minute)

	entry := insertEntry(t, pool, home, milk, sam)
	var (
		purchasedToday       bool
		expiresOn            *time.Time
		createdAt, updatedAt time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT purchased_on = CURRENT_DATE, expires_on, created_at, updated_at
		FROM pantry_entries WHERE id = $1`, entry).Scan(&purchasedToday, &expiresOn, &createdAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if !purchasedToday {
		t.Error("purchased_on is not defaulted to today")
	}
	if expiresOn != nil {
		t.Errorf("expires_on = %v, want NULL", expiresOn)
	}
	if createdAt.Before(before) || updatedAt.Before(before) {
		t.Errorf("entry timestamps not defaulted to now: created_at %v, updated_at %v", createdAt, updatedAt)
	}

	event := insertEvent(t, pool, home, entry, milk, sam, "entry_created")
	var (
		changes, snapshot string
		occurredAt        time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT changes::text, snapshot::text, occurred_at FROM item_events WHERE id = $1`, event,
	).Scan(&changes, &snapshot, &occurredAt); err != nil {
		t.Fatal(err)
	}
	if changes != "{}" {
		t.Errorf("changes = %s, want {}", changes)
	}
	if snapshot != `{"name": "Milk", "level": "full"}` {
		t.Errorf("snapshot = %s", snapshot)
	}
	if occurredAt.Before(before) {
		t.Errorf("occurred_at not defaulted to now: %v", occurredAt)
	}

	var sentAt time.Time
	if err := pool.QueryRow(ctx, `
		INSERT INTO expiry_notifications (entry_id, user_id, expires_on) VALUES ($1, $2, CURRENT_DATE)
		RETURNING sent_at`, entry, sam).Scan(&sentAt); err != nil {
		t.Fatalf("inserting notification: %v", err)
	}
	if sentAt.Before(before) {
		t.Errorf("sent_at not defaulted to now: %v", sentAt)
	}
}

func TestEntryRequiredColumns(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)

	_, err := pool.Exec(ctx, `
		INSERT INTO pantry_entries (household_id, catalog_item_id, location_id, level)
		VALUES ($1, $2, $3, 'full')`, home, milk, locFridge)
	wantSQLState(t, err, sqlStateNotNull, "entry without id")

	_, err = pool.Exec(ctx, `
		INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id)
		VALUES (uuidv7(), $1, $2, $3)`, home, milk, locFridge)
	wantSQLState(t, err, sqlStateNotNull, "entry without level")

	_, err = pool.Exec(ctx, `
		INSERT INTO item_events (household_id, entry_id, catalog_item_id, type, snapshot)
		VALUES ($1, uuidv7(), $2, 'entry_created', '{}')`, home, milk)
	wantSQLState(t, err, sqlStateNotNull, "event without id")

	_, err = pool.Exec(ctx, `
		INSERT INTO item_events (id, household_id, entry_id, catalog_item_id, type)
		VALUES (uuidv7(), $1, uuidv7(), $2, 'entry_created')`, home, milk)
	wantSQLState(t, err, sqlStateNotNull, "event without snapshot")
}

func TestEntryUnknownEnumValues(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)

	_, err := pool.Exec(ctx, `
		INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level)
		VALUES (uuidv7(), $1, $2, $3, 'empty')`, home, milk, locFridge)
	wantSQLState(t, err, sqlStateInvalidText, "level 'empty'")

	_, err = pool.Exec(ctx, `
		INSERT INTO item_events (id, household_id, entry_id, catalog_item_id, type, snapshot)
		VALUES (uuidv7(), $1, uuidv7(), $2, 'renamed', '{}')`, home, milk)
	wantSQLState(t, err, sqlStateInvalidText, "type 'renamed'")
}

func TestEntryUnknownReferences(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)
	const missing = "00000000-0000-7000-8000-000000000000"

	insert := `INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level, created_by)
	           VALUES (uuidv7(), $1, $2, $3, 'full', $4)`
	_, err := pool.Exec(ctx, insert, missing, milk, locFridge, nil)
	wantSQLState(t, err, sqlStateForeignKey, "entry in unknown household")
	_, err = pool.Exec(ctx, insert, home, missing, locFridge, nil)
	wantSQLState(t, err, sqlStateForeignKey, "entry for unknown catalog item")
	_, err = pool.Exec(ctx, insert, home, milk, 999, nil)
	wantSQLState(t, err, sqlStateForeignKey, "entry in unknown location")
	_, err = pool.Exec(ctx, insert, home, milk, locFridge, missing)
	wantSQLState(t, err, sqlStateForeignKey, "entry created by unknown user")

	_, err = pool.Exec(ctx, `
		INSERT INTO expiry_notifications (entry_id, user_id, expires_on) VALUES (uuidv7(), $1, CURRENT_DATE)`, missing)
	wantSQLState(t, err, sqlStateForeignKey, "notification for unknown user")
}

// Events and notifications have no foreign key to entries or catalog items.
func TestEventForEntryThatNeverExisted(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	home := insertHousehold(t, pool, "Home", nil)
	ghostEntry, ghostItem := newID(t, pool), newID(t, pool)

	insertEvent(t, pool, home, ghostEntry, ghostItem, nil, "level_changed")
	if _, err := pool.Exec(ctx, `
		INSERT INTO expiry_notifications (entry_id, user_id, expires_on) VALUES ($1, $2, CURRENT_DATE)`,
		ghostEntry, sam); err != nil {
		t.Errorf("notification for an entry that never existed: %v", err)
	}
}

func TestDeletingEntryKeepsEvents(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)
	entry := insertEntry(t, pool, home, milk, nil)
	insertEvent(t, pool, home, entry, milk, nil, "entry_created")
	insertEvent(t, pool, home, entry, milk, nil, "level_changed")

	if _, err := pool.Exec(ctx, `DELETE FROM pantry_entries WHERE id = $1`, entry); err != nil {
		t.Fatalf("deleting entry: %v", err)
	}
	insertEvent(t, pool, home, entry, milk, nil, "entry_deleted")

	if n := count(t, pool, `SELECT count(*) FROM pantry_entries WHERE id = $1`, entry); n != 0 {
		t.Errorf("entry still present")
	}
	if n := count(t, pool, `SELECT count(*) FROM item_events WHERE entry_id = $1`, entry); n != 3 {
		t.Errorf("%d events point at the deleted entry, want 3", n)
	}
}

func TestDeletingHouseholdRemovesEntriesAndEvents(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)
	cabin := insertHousehold(t, pool, "Cabin", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)
	for _, h := range []string{home, cabin} {
		for range 2 {
			e := insertEntry(t, pool, h, milk, nil)
			insertEvent(t, pool, h, e, milk, nil, "entry_created")
		}
	}

	if _, err := pool.Exec(ctx, `DELETE FROM households WHERE id = $1`, home); err != nil {
		t.Fatalf("deleting household: %v", err)
	}
	for _, c := range []struct {
		table, household string
		want             int
	}{
		{"pantry_entries", home, 0},
		{"item_events", home, 0},
		{"pantry_entries", cabin, 2},
		{"item_events", cabin, 2},
	} {
		if n := count(t, pool, `SELECT count(*) FROM `+c.table+` WHERE household_id = $1`, c.household); n != c.want {
			t.Errorf("%s rows for household %s = %d, want %d", c.table, c.household, n, c.want)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM catalog_items WHERE id = $1`, milk); n != 1 {
		t.Errorf("built-in item removed with the household")
	}
}

// Deleting a household cascades both to its custom catalog items and to the
// entries pointing at them. pantry_entries.catalog_item_id has NO ACTION
// (checked at the end of the statement), so this succeeds.
func TestDeletingHouseholdWithEntriesForCustomItems(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)
	cabin := insertHousehold(t, pool, "Cabin", nil)
	jam := mustAddItem(t, pool, home, "Grandma's jam", catProduce, locPantry, nil)
	cabinJam := mustAddItem(t, pool, cabin, "Grandma's jam", catProduce, locPantry, nil)
	insertEntry(t, pool, home, jam, nil)
	insertEntry(t, pool, home, jam, nil)
	insertEntry(t, pool, cabin, cabinJam, nil)

	if _, err := pool.Exec(ctx, `DELETE FROM households WHERE id = $1`, home); err != nil {
		t.Fatalf("deleting household with entries for its custom items: %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM catalog_items WHERE id = $1`, jam); n != 0 {
		t.Errorf("custom item survived its household")
	}
	if n := count(t, pool, `SELECT count(*) FROM pantry_entries WHERE household_id = $1`, home); n != 0 {
		t.Errorf("%d entries survived their household", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM pantry_entries WHERE catalog_item_id = $1`, cabinJam); n != 1 {
		t.Errorf("other household's entries = %d, want 1", n)
	}
}

func TestDeletingCatalogItemWithEntriesFails(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)
	jam := mustAddItem(t, pool, home, "Jam", catProduce, locPantry, nil)
	insertEntry(t, pool, home, milk, nil)
	insertEntry(t, pool, home, jam, nil)

	for name, id := range map[string]string{"built-in": milk, "custom": jam} {
		_, err := pool.Exec(ctx, `DELETE FROM catalog_items WHERE id = $1`, id)
		wantSQLState(t, err, sqlStateForeignKey, "deleting "+name+" item with entries")
	}
}

func TestDeletingUserClearsEntryAndEventActors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	alex := insertUser(t, pool, "fb-alex")
	home := insertHousehold(t, pool, "Home", sam)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)

	alexEntry := insertEntry(t, pool, home, milk, alex)
	samEntry := insertEntry(t, pool, home, milk, sam)
	alexEvent := insertEvent(t, pool, home, alexEntry, milk, alex, "entry_created")
	samEvent := insertEvent(t, pool, home, samEntry, milk, sam, "entry_created")
	for _, user := range []string{alex, sam} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO expiry_notifications (entry_id, user_id, expires_on) VALUES ($1, $2, CURRENT_DATE)`,
			alexEntry, user); err != nil {
			t.Fatal(err)
		}
	}

	// Alex created no household, so deleting Alex is allowed.
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, alex); err != nil {
		t.Fatalf("deleting user: %v", err)
	}

	var createdBy, updatedBy, actor *string
	if err := pool.QueryRow(ctx, `SELECT created_by::text, updated_by::text FROM pantry_entries WHERE id = $1`, alexEntry).
		Scan(&createdBy, &updatedBy); err != nil {
		t.Fatalf("reading Alex's entry: %v", err)
	}
	if createdBy != nil || updatedBy != nil {
		t.Errorf("Alex's entry created_by = %v, updated_by = %v; want NULL", createdBy, updatedBy)
	}
	if err := pool.QueryRow(ctx, `SELECT actor_id::text FROM item_events WHERE id = $1`, alexEvent).Scan(&actor); err != nil {
		t.Fatalf("reading Alex's event: %v", err)
	}
	if actor != nil {
		t.Errorf("Alex's event actor_id = %v, want NULL", *actor)
	}
	if n := count(t, pool, `SELECT count(*) FROM expiry_notifications WHERE user_id = $1`, alex); n != 0 {
		t.Errorf("%d notifications left for the deleted user", n)
	}

	// Sam's rows are untouched.
	if n := count(t, pool, `SELECT count(*) FROM pantry_entries WHERE id = $1 AND created_by = $2 AND updated_by = $2`, samEntry, sam); n != 1 {
		t.Errorf("Sam's entry lost its creator")
	}
	if n := count(t, pool, `SELECT count(*) FROM item_events WHERE id = $1 AND actor_id = $2`, samEvent, sam); n != 1 {
		t.Errorf("Sam's event lost its actor")
	}
	if n := count(t, pool, `SELECT count(*) FROM expiry_notifications WHERE user_id = $1`, sam); n != 1 {
		t.Errorf("Sam has %d notifications, want 1", n)
	}
}

func TestExpiryNotificationsKey(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	sam := insertUser(t, pool, "fb-sam")
	entry := newID(t, pool)
	insert := `INSERT INTO expiry_notifications (entry_id, user_id, expires_on) VALUES ($1, $2, $3)`

	if _, err := pool.Exec(ctx, insert, entry, sam, "2026-11-10"); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, insert, entry, sam, "2026-11-10")
	wantSQLState(t, err, sqlStateUnique, "same alert twice")
	if _, err := pool.Exec(ctx, insert, entry, sam, "2026-11-14"); err != nil {
		t.Errorf("alert for an edited expiry date: %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM expiry_notifications`); n != 2 {
		t.Errorf("%d notifications, want 2", n)
	}
}

// A retried event insert (#19) is ignored rather than failing.
func TestEventRetryIsIgnored(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	home := insertHousehold(t, pool, "Home", nil)
	milk := mustAddItem(t, pool, nil, "Milk", catDairy, locFridge, 7)
	entry := insertEntry(t, pool, home, milk, nil)
	event := insertEvent(t, pool, home, entry, milk, nil, "entry_created")

	tag, err := pool.Exec(ctx, `
		INSERT INTO item_events (id, household_id, entry_id, catalog_item_id, type, snapshot)
		VALUES ($1, $2, $3, $4, 'level_changed', '{"retry": true}')
		ON CONFLICT (id) DO NOTHING`, event, home, entry, milk)
	if err != nil {
		t.Fatalf("retrying event insert: %v", err)
	}
	if tag.RowsAffected() != 0 {
		t.Errorf("retry inserted %d rows, want 0", tag.RowsAffected())
	}
	var typ string
	if err := pool.QueryRow(ctx, `SELECT type::text FROM item_events WHERE id = $1`, event).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if typ != "entry_created" {
		t.Errorf("event type = %s after retry, want entry_created", typ)
	}
	if n := count(t, pool, `SELECT count(*) FROM item_events`); n != 1 {
		t.Errorf("%d events, want 1", n)
	}
}

// TestEntriesMigrationDownUp rolls back this migration (and any later ones),
// checks every object it created is gone, then migrates up again.
func TestEntriesMigrationDownUp(t *testing.T) {
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
		if filepath.Base(s.Path) == entriesMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", entriesMigration)
	}

	type objects struct{ Entries, Events, Notifications, StockLevel, EventType bool }
	exist := func() objects {
		t.Helper()
		var o objects
		if err := pool.QueryRow(ctx, `
			SELECT to_regclass('pantry_entries') IS NOT NULL,
			       to_regclass('item_events') IS NOT NULL,
			       to_regclass('expiry_notifications') IS NOT NULL,
			       to_regtype('stock_level') IS NOT NULL,
			       to_regtype('event_type') IS NOT NULL`,
		).Scan(&o.Entries, &o.Events, &o.Notifications, &o.StockLevel, &o.EventType); err != nil {
			t.Fatal(err)
		}
		return o
	}
	all := objects{true, true, true, true, true}

	if got := exist(); got != all {
		t.Fatalf("before down: %+v; want all present", got)
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
	if got := exist(); got != all {
		t.Fatalf("after up: %+v; want all created", got)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to latest: %v", err)
	}
}
