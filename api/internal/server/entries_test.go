package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/auth/authtest"
	"github.com/marboleda/shelfkin/api/internal/events"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// fakeAPI's pantry entry endpoints, so it keeps satisfying
// StrictServerInterface.

func (fakeAPI) CreateEntry(context.Context, openapi.CreateEntryRequestObject) (openapi.CreateEntryResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) UpdateEntry(context.Context, openapi.UpdateEntryRequestObject) (openapi.UpdateEntryResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) DeleteEntry(context.Context, openapi.DeleteEntryRequestObject) (openapi.DeleteEntryResponseObject, error) {
	return nil, errNotImplemented
}

func TestParseUUIDv7(t *testing.T) {
	v7 := uuid.Must(uuid.NewV7()).String()
	tests := []struct {
		in string
		ok bool
	}{
		{v7, true},
		{strings.ToUpper(v7), true},
		{"0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80", true},
		{uuid.New().String(), false},                    // version 4
		{"0190f3a1-7c2e-1b3d-9a4f-2c1e5d6b7a80", false}, // version 1
		{"00000000-0000-0000-0000-000000000000", false},
		{strings.ReplaceAll(v7, "-", ""), false},
		{"{" + v7 + "}", false},
		{"urn:uuid:" + v7, false},
		{"not-a-uuid", false},
		{"", false},
	}
	for _, tt := range tests {
		if _, ok := ParseUUIDv7(tt.in); ok != tt.ok {
			t.Errorf("ParseUUIDv7(%q) ok = %v, want %v", tt.in, ok, tt.ok)
		}
	}
}

// fixedNow is the clock of the entry tests: 21:00 on 2026-11-04 in Toronto,
// already 2026-11-05 in UTC.
var fixedNow = time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)

// newEntryServer is newMeServer with the API's clock fixed at fixedNow.
func newEntryServer(t *testing.T) *meServer {
	t.Helper()
	pool := dbtest.New(t)
	logger, _ := newLogger()
	v := authtest.NewVerifier()
	api := NewAPI(pool, logger)
	api.now = func() time.Time { return fixedNow }
	url := serve(t, NewHandler(api, auth.NewAuthenticator(v, pool), logger, nil))
	return &meServer{url: url, pool: pool, verifier: v}
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }

// pantryEntry is a decoded PantryEntry response.
type pantryEntry struct {
	ID            string    `json:"id"`
	CatalogItemID string    `json:"catalogItemId"`
	LocationID    int       `json:"locationId"`
	Level         string    `json:"level"`
	PurchasedOn   string    `json:"purchasedOn"`
	ExpiresOn     *string   `json:"expiresOn"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// itemEvent is an item_events row, read as the superuser.
type itemEvent struct {
	ID            string
	HouseholdID   string
	EntryID       string
	CatalogItemID string
	ActorID       string
	Type          string
	Changes       map[string]events.Change
	Snapshot      events.Snapshot
	OccurredAt    time.Time
}

// entryFixture is a household with members and catalog item IDs.
type entryFixture struct {
	*meServer
	sam, alex     string // tokens; Sam owns home, Alex is a member
	samID, alexID string
	home          string // household ID
	milk, salt    string // built-in items: Milk (fridge, 7 days), Salt (spice rack, no shelf life)
}

func newEntryFixture(t *testing.T) *entryFixture {
	t.Helper()
	s := newEntryServer(t)
	f := &entryFixture{meServer: s}
	f.sam = s.verifier.Issue(auth.Token{UID: "sam", Name: "Sam"})
	f.alex = s.verifier.Issue(auth.Token{UID: "alex", Name: "Alex"})
	f.samID = s.userID(t, f.sam)
	f.alexID = s.userID(t, f.alex)
	f.home = s.create(t, f.sam, "Home").ID
	s.exec(t, `INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'member')`, f.home, f.alexID)
	f.milk = f.builtin(t, "Milk")
	f.salt = f.builtin(t, "Salt")
	return f
}

func (f *entryFixture) builtin(t *testing.T, name string) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT id::text FROM catalog_items WHERE household_id IS NULL AND name = $1`, name).Scan(&id); err != nil {
		t.Fatalf("built-in %s: %v", name, err)
	}
	return id
}

// customItem inserts a custom catalog item for household and returns its ID.
func (f *entryFixture) customItem(t *testing.T, household, name string) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO catalog_items (household_id, name, default_category_id, default_location_id, shelf_life_days)
		 VALUES ($1, $2, 1, 1, 3) RETURNING id::text`, household, name).Scan(&id); err != nil {
		t.Fatalf("custom item %s: %v", name, err)
	}
	return id
}

func jsonBody(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func entriesPath(household string) string { return "/households/" + household + "/entries" }

func entryPath(household, entry string) string { return entriesPath(household) + "/" + entry }

// post creates an entry and returns the response.
func (f *entryFixture) post(t *testing.T, token, household string, body map[string]any) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodPost, entriesPath(household), token, jsonBody(t, body))
}

// createEntry creates an entry of item in home as Sam, with extra fields,
// and returns it and its event ID.
func (f *entryFixture) createEntry(t *testing.T, item string, extra map[string]any) (pantryEntry, string) {
	t.Helper()
	body := map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": item}
	for k, v := range extra {
		body[k] = v
	}
	resp, b := f.post(t, f.sam, f.home, body)
	var e pantryEntry
	wantStatus(t, resp, b, http.StatusCreated, &e)
	return e, body["eventId"].(string)
}

func (f *entryFixture) patch(t *testing.T, token, household, entry string, body map[string]any) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodPatch, entryPath(household, entry), token, jsonBody(t, body))
}

// update patches an entry in home as token and returns the 200 response.
func (f *entryFixture) update(t *testing.T, token, entry string, body map[string]any) pantryEntry {
	t.Helper()
	resp, b := f.patch(t, token, f.home, entry, body)
	var e pantryEntry
	wantStatus(t, resp, b, http.StatusOK, &e)
	return e
}

func (f *entryFixture) del(t *testing.T, token, household, entry, eventID string) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodDelete, entryPath(household, entry)+"?eventId="+eventID, token, "")
}

// event reads an item_events row by ID, bypassing row-level security.
func (f *entryFixture) event(t *testing.T, id string) itemEvent {
	t.Helper()
	var e itemEvent
	var changes, snapshot []byte
	var actor *string
	err := f.pool.QueryRow(context.Background(), `
		SELECT id::text, household_id::text, entry_id::text, catalog_item_id::text, actor_id::text,
		       type::text, changes, snapshot, occurred_at
		FROM item_events WHERE id = $1`, id).
		Scan(&e.ID, &e.HouseholdID, &e.EntryID, &e.CatalogItemID, &actor, &e.Type, &changes, &snapshot, &e.OccurredAt)
	if err != nil {
		t.Fatalf("event %s: %v", id, err)
	}
	if actor != nil {
		e.ActorID = *actor
	}
	if err := json.Unmarshal(changes, &e.Changes); err != nil {
		t.Fatalf("changes %s: %v", changes, err)
	}
	if err := json.Unmarshal(snapshot, &e.Snapshot); err != nil {
		t.Fatalf("snapshot %s: %v", snapshot, err)
	}
	// The snapshot has exactly the six keys.
	var raw map[string]any
	_ = json.Unmarshal(snapshot, &raw)
	if len(raw) != 6 {
		t.Errorf("snapshot %s: want exactly name, catalog_item_id, level, location_id, purchased_on, expires_on", snapshot)
	}
	return e
}

func (f *entryFixture) events(t *testing.T, entry string) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM item_events WHERE entry_id = $1`, entry)
}

func strp(s string) *string { return &s }

func eqStrp(a, b *string) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

func fmtStrp(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

// wantSnapshot checks a snapshot against the entry's state.
func wantSnapshot(t *testing.T, got events.Snapshot, name string, e pantryEntry) {
	t.Helper()
	want := events.Snapshot{Name: name, CatalogItemID: e.CatalogItemID, Level: e.Level,
		LocationID: e.LocationID, PurchasedOn: e.PurchasedOn, ExpiresOn: e.ExpiresOn}
	if got.Name != want.Name || got.CatalogItemID != want.CatalogItemID || got.Level != want.Level ||
		got.LocationID != want.LocationID || got.PurchasedOn != want.PurchasedOn || !eqStrp(got.ExpiresOn, want.ExpiresOn) {
		t.Errorf("snapshot = %+v (expires %s), want %+v (expires %s)", got, fmtStrp(got.ExpiresOn), want, fmtStrp(want.ExpiresOn))
	}
}

// wantChange checks that changes is exactly {column: {from, to}}. JSON
// numbers decode as float64.
func wantChange(t *testing.T, changes map[string]events.Change, column string, from, to any) {
	t.Helper()
	c, ok := changes[column]
	if len(changes) != 1 || !ok || c.From != from || c.To != to {
		t.Errorf("changes = %v, want {%s: {from: %v, to: %v}}", changes, column, from, to)
	}
}

func TestCreateEntryDefaults(t *testing.T) {
	f := newEntryFixture(t)
	f.user(t, http.MethodPatch, f.sam, `{"timezone":"America/Toronto"}`)

	id, eventID := newID(), newID()
	before := time.Now().Add(-time.Minute)
	resp, b := f.post(t, f.sam, f.home, map[string]any{"id": id, "eventId": eventID, "catalogItemId": f.milk})
	var raw map[string]any
	wantStatus(t, resp, b, http.StatusCreated, &raw)
	if len(raw) != 8 {
		t.Errorf("POST entries = %s, want exactly the eight PantryEntry fields", b)
	}
	var e pantryEntry
	wantStatus(t, resp, b, http.StatusCreated, &e)
	// Milk: default location Fridge (2), 7 days shelf life. 21:00 in Toronto
	// on Nov 4 is Nov 5 in UTC; the purchase date is the caller's local date.
	want := pantryEntry{ID: id, CatalogItemID: f.milk, LocationID: 2, Level: "full",
		PurchasedOn: "2026-11-04", ExpiresOn: strp("2026-11-11")}
	if e.ID != want.ID || e.CatalogItemID != want.CatalogItemID || e.LocationID != want.LocationID ||
		e.Level != want.Level || e.PurchasedOn != want.PurchasedOn || !eqStrp(e.ExpiresOn, want.ExpiresOn) {
		t.Errorf("POST entries = %s, want %+v (expires 2026-11-11)", b, want)
	}
	if e.CreatedAt.Before(before) || !e.CreatedAt.Equal(e.UpdatedAt) {
		t.Errorf("createdAt %v, updatedAt %v: want about now and equal", e.CreatedAt, e.UpdatedAt)
	}
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1 AND household_id = $2
		AND created_by = $3 AND updated_by = $3`, id, f.home, f.samID); n != 1 {
		t.Errorf("entry row with household and created_by/updated_by = caller: %d, want 1", n)
	}

	// One entry_created event with every field set.
	if n := f.events(t, id); n != 1 {
		t.Fatalf("%d events for the entry, want 1", n)
	}
	ev := f.event(t, eventID)
	if ev.HouseholdID != f.home || ev.EntryID != id || ev.CatalogItemID != f.milk ||
		ev.ActorID != f.samID || ev.Type != "entry_created" || len(ev.Changes) != 0 {
		t.Errorf("event = %+v, want entry_created in home for the entry by Sam with changes {}", ev)
	}
	if ev.OccurredAt.Before(before) || ev.OccurredAt.After(time.Now().Add(time.Minute)) {
		t.Errorf("occurred_at = %v, want the server's time now", ev.OccurredAt)
	}
	wantSnapshot(t, ev.Snapshot, "Milk", e)

	// A UTC user at the same instant gets Nov 5.
	resp, b = f.post(t, f.alex, f.home, map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk})
	wantStatus(t, resp, b, http.StatusCreated, &e)
	if e.PurchasedOn != "2026-11-05" || !eqStrp(e.ExpiresOn, strp("2026-11-12")) {
		t.Errorf("UTC user: purchasedOn %s, expiresOn %s, want 2026-11-05 and 2026-11-12", e.PurchasedOn, fmtStrp(e.ExpiresOn))
	}

	// An item with no shelf life gets no expiry.
	e, _ = f.createEntry(t, f.salt, nil)
	if e.ExpiresOn != nil || e.LocationID != 4 {
		t.Errorf("Salt: expiresOn %s, location %d; want null and 4 (spice rack)", fmtStrp(e.ExpiresOn), e.LocationID)
	}

	// Explicit null means no expiry, even with a shelf life.
	e, ev2 := f.createEntry(t, f.milk, map[string]any{"expiresOn": nil})
	if e.ExpiresOn != nil {
		t.Errorf("expiresOn: null gave %s, want null", fmtStrp(e.ExpiresOn))
	}
	if got := f.event(t, ev2).Snapshot.ExpiresOn; got != nil {
		t.Errorf("snapshot expires_on = %s, want null", *got)
	}

	// The suggested expiry follows an explicit purchase date. A future purchase
	// date and an expiry before the purchase date are both accepted.
	e, _ = f.createEntry(t, f.milk, map[string]any{"purchasedOn": "2027-01-10"})
	if e.PurchasedOn != "2027-01-10" || !eqStrp(e.ExpiresOn, strp("2027-01-17")) {
		t.Errorf("purchasedOn 2027-01-10: got %s / %s, want expiry 2027-01-17", e.PurchasedOn, fmtStrp(e.ExpiresOn))
	}
	e, _ = f.createEntry(t, f.milk, map[string]any{"purchasedOn": "2026-11-01", "expiresOn": "2026-10-30", "level": "low", "locationId": 3})
	if e.PurchasedOn != "2026-11-01" || !eqStrp(e.ExpiresOn, strp("2026-10-30")) || e.Level != "low" || e.LocationID != 3 {
		t.Errorf("explicit fields: got %+v", e)
	}

	// The household's own default location for a built-in item wins.
	f.exec(t, `INSERT INTO household_item_defaults (household_id, catalog_item_id, location_id) VALUES ($1, $2, 3)`, f.home, f.milk)
	e, _ = f.createEntry(t, f.milk, nil)
	if e.LocationID != 3 {
		t.Errorf("with a household default of Freezer: location %d, want 3", e.LocationID)
	}

	// A custom item of this household works, with its own defaults.
	custom := f.customItem(t, f.home, "Grandma's jam")
	e, evc := f.createEntry(t, custom, map[string]any{"purchasedOn": "2026-11-04"})
	if e.LocationID != 1 || !eqStrp(e.ExpiresOn, strp("2026-11-07")) {
		t.Errorf("custom item: location %d, expires %s; want 1 and 2026-11-07", e.LocationID, fmtStrp(e.ExpiresOn))
	}
	wantSnapshot(t, f.event(t, evc).Snapshot, "Grandma's jam", e)
}

func TestCreateEntryValidation(t *testing.T) {
	f := newEntryFixture(t)
	bob := f.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	bobHome := f.create(t, bob, "Bob's").ID
	bobsItem := f.customItem(t, bobHome, "Bob's sauce")

	tests := []struct {
		name string
		body map[string]any
		code string
	}{
		{"missing id", map[string]any{"eventId": newID(), "catalogItemId": f.milk}, CodeInvalidID},
		{"missing eventId", map[string]any{"id": newID(), "catalogItemId": f.milk}, CodeInvalidID},
		{"v4 id", map[string]any{"id": uuid.New().String(), "eventId": newID(), "catalogItemId": f.milk}, CodeInvalidID},
		{"v4 eventId", map[string]any{"id": newID(), "eventId": uuid.New().String(), "catalogItemId": f.milk}, CodeInvalidID},
		{"garbage id", map[string]any{"id": "abc", "eventId": newID(), "catalogItemId": f.milk}, CodeInvalidID},
		{"nil uuid", map[string]any{"id": uuid.Nil.String(), "eventId": newID(), "catalogItemId": f.milk}, CodeInvalidID},
		{"other household's item", map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": bobsItem}, CodeInvalidCatalogItem},
		{"no such item", map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": newID()}, CodeInvalidCatalogItem},
		{"missing item", map[string]any{"id": newID(), "eventId": newID()}, CodeInvalidCatalogItem},
		{"no such location", map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk, "locationId": 99}, CodeInvalidLocation},
		{"location 0", map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk, "locationId": 0}, CodeInvalidLocation},
		{"huge location", map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk, "locationId": 70000}, CodeInvalidLocation},
		{"bad level", map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk, "level": "empty"}, CodeInvalidLevel},
		{"bad date", map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk, "purchasedOn": "11/04/2026"}, CodeBadRequest},
		{"wrong type", map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk, "locationId": "fridge"}, CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, b := f.post(t, f.sam, f.home, tt.body)
			wantError(t, resp, b, http.StatusBadRequest, tt.code)
		})
	}
	if n := f.count(t, `SELECT count(*) FROM pantry_entries`); n != 0 {
		t.Errorf("%d entries after invalid requests, want 0", n)
	}
	if n := f.count(t, `SELECT count(*) FROM item_events`); n != 0 {
		t.Errorf("%d events after invalid requests, want 0", n)
	}
	// Bob's item is still deletable: nothing points at it.
	f.exec(t, `DELETE FROM catalog_items WHERE id = $1`, bobsItem)
}

func TestUpdateEntryEvents(t *testing.T) {
	f := newEntryFixture(t)
	e, _ := f.createEntry(t, f.milk, map[string]any{"purchasedOn": "2026-11-01"}) // expires 2026-11-08
	ctx := context.Background()
	lastUpdate := func() (time.Time, string) {
		var at time.Time
		var by string
		if err := f.pool.QueryRow(ctx, `SELECT updated_at, updated_by::text FROM pantry_entries WHERE id = $1`, e.ID).Scan(&at, &by); err != nil {
			t.Fatal(err)
		}
		return at, by
	}

	steps := []struct {
		name       string
		token      string
		field      string
		value      any
		eventType  string
		column     string
		from, to   any
		wantEntry  func(*pantryEntry)
		actorIsSam bool
	}{
		{"level", f.alex, "level", "low", "level_changed", "level", "full", "low",
			func(p *pantryEntry) { p.Level = "low" }, false},
		{"location", f.sam, "locationId", 3, "location_changed", "location_id", float64(2), float64(3),
			func(p *pantryEntry) { p.LocationID = 3 }, true},
		{"purchase date", f.alex, "purchasedOn", "2026-11-03", "purchase_date_changed", "purchased_on", "2026-11-01", "2026-11-03",
			func(p *pantryEntry) { p.PurchasedOn = "2026-11-03" }, false}, // expiry not recomputed
		{"expiry", f.sam, "expiresOn", "2026-11-20", "expiry_changed", "expires_on", "2026-11-08", "2026-11-20",
			func(p *pantryEntry) { p.ExpiresOn = strp("2026-11-20") }, true},
		{"clear expiry", f.alex, "expiresOn", nil, "expiry_changed", "expires_on", "2026-11-20", nil,
			func(p *pantryEntry) { p.ExpiresOn = nil }, false},
		{"set expiry again", f.alex, "expiresOn", "2026-11-09", "expiry_changed", "expires_on", nil, "2026-11-09",
			func(p *pantryEntry) { p.ExpiresOn = strp("2026-11-09") }, false},
		{"out", f.sam, "level", "out", "level_changed", "level", "low", "out",
			func(p *pantryEntry) { p.Level = "out" }, true},
	}
	want := e
	for i, st := range steps {
		eventID := newID()
		prevAt, _ := lastUpdate()
		got := f.update(t, st.token, e.ID, map[string]any{"eventId": eventID, st.field: st.value})
		st.wantEntry(&want)
		if got.Level != want.Level || got.LocationID != want.LocationID || got.PurchasedOn != want.PurchasedOn ||
			!eqStrp(got.ExpiresOn, want.ExpiresOn) || got.ID != e.ID || got.CatalogItemID != f.milk || !got.CreatedAt.Equal(e.CreatedAt) {
			t.Fatalf("%s: PATCH = %+v (expires %s), want %+v (expires %s)", st.name, got, fmtStrp(got.ExpiresOn), want, fmtStrp(want.ExpiresOn))
		}
		at, by := lastUpdate()
		actor := f.alexID
		if st.actorIsSam {
			actor = f.samID
		}
		if !at.After(prevAt) || by != actor || !got.UpdatedAt.Equal(at.UTC().Truncate(time.Microsecond)) {
			t.Errorf("%s: updated_at %v (was %v), updated_by %s; want later, by %s, and in the response", st.name, at, prevAt, by, actor)
		}
		ev := f.event(t, eventID)
		if ev.Type != st.eventType || ev.ActorID != actor || ev.EntryID != e.ID || ev.HouseholdID != f.home || ev.CatalogItemID != f.milk {
			t.Errorf("%s: event = %+v, want %s by %s", st.name, ev, st.eventType, actor)
		}
		wantChange(t, ev.Changes, st.column, st.from, st.to)
		wantSnapshot(t, ev.Snapshot, "Milk", want)
		if n := f.events(t, e.ID); n != i+2 {
			t.Errorf("%s: %d events, want %d", st.name, n, i+2)
		}
	}

	// Level out keeps the entry.
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1 AND level = 'out'`, e.ID); n != 1 {
		t.Errorf("entry at level out: %d rows, want 1", n)
	}

	// Setting a field to its current value: 200, no event, updated_at unchanged.
	at, _ := lastUpdate()
	n := f.events(t, e.ID)
	for _, body := range []map[string]any{
		{"level": "out"}, {"locationId": 3}, {"purchasedOn": "2026-11-03"}, {"expiresOn": "2026-11-09"},
	} {
		body["eventId"] = newID()
		got := f.update(t, f.alex, e.ID, body)
		if got.Level != "out" || !got.UpdatedAt.Equal(at.UTC().Truncate(time.Microsecond)) {
			t.Errorf("no-op %v: got %+v, want the unchanged entry", body, got)
		}
		if f.count(t, `SELECT count(*) FROM item_events WHERE id = $1`, body["eventId"]) != 0 {
			t.Errorf("no-op %v wrote an event", body)
		}
	}
	f.update(t, f.alex, e.ID, map[string]any{"eventId": newID(), "expiresOn": "2026-11-09"})
	if got, _ := lastUpdate(); !got.Equal(at) || f.events(t, e.ID) != n {
		t.Errorf("after no-op updates: updated_at %v (was %v), %d events (was %d)", got, at, f.events(t, e.ID), n)
	}
	// Clearing an expiry that is already empty is a no-op too.
	f.update(t, f.alex, e.ID, map[string]any{"eventId": newID(), "expiresOn": nil})
	before := f.events(t, e.ID)
	f.update(t, f.alex, e.ID, map[string]any{"eventId": newID(), "expiresOn": nil})
	if f.events(t, e.ID) != before {
		t.Errorf("clearing an empty expiry wrote an event")
	}
}

func TestUpdateEntryValidation(t *testing.T) {
	f := newEntryFixture(t)
	e, _ := f.createEntry(t, f.milk, nil)
	tests := []struct {
		name string
		body map[string]any
		code string
	}{
		{"no field", map[string]any{"eventId": newID()}, CodeOneFieldPerUpdate},
		{"two fields", map[string]any{"eventId": newID(), "level": "low", "locationId": 1}, CodeOneFieldPerUpdate},
		{"two with null expiry", map[string]any{"eventId": newID(), "level": "low", "expiresOn": nil}, CodeOneFieldPerUpdate},
		{"four fields", map[string]any{"eventId": newID(), "level": "low", "locationId": 1, "purchasedOn": "2026-11-01", "expiresOn": "2026-11-02"}, CodeOneFieldPerUpdate},
		{"missing eventId", map[string]any{"level": "low"}, CodeInvalidID},
		{"v4 eventId", map[string]any{"eventId": uuid.New().String(), "level": "low"}, CodeInvalidID},
		{"bad level", map[string]any{"eventId": newID(), "level": "empty"}, CodeInvalidLevel},
		{"no such location", map[string]any{"eventId": newID(), "locationId": 42}, CodeInvalidLocation},
		{"bad date", map[string]any{"eventId": newID(), "expiresOn": "soon"}, CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, b := f.patch(t, f.sam, f.home, e.ID, tt.body)
			wantError(t, resp, b, http.StatusBadRequest, tt.code)
		})
	}
	if n := f.events(t, e.ID); n != 1 {
		t.Errorf("%d events after invalid updates, want 1", n)
	}
	resp, b := f.req(t, http.MethodPatch, entriesPath(f.home)+"/not-a-uuid", f.sam, `{"eventId":"`+newID()+`","level":"low"}`)
	wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)
}

func TestDeleteEntry(t *testing.T) {
	f := newEntryFixture(t)
	e, _ := f.createEntry(t, f.milk, nil)
	e = f.update(t, f.sam, e.ID, map[string]any{"eventId": newID(), "level": "half"})

	eventID := newID()
	resp, b := f.del(t, f.alex, f.home, e.ID, eventID)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1`, e.ID); n != 0 {
		t.Errorf("entry still there after DELETE")
	}
	ev := f.event(t, eventID)
	if ev.Type != "entry_deleted" || ev.ActorID != f.alexID || ev.EntryID != e.ID || ev.HouseholdID != f.home ||
		ev.CatalogItemID != f.milk || len(ev.Changes) != 0 {
		t.Errorf("event = %+v, want entry_deleted by Alex with changes {}", ev)
	}
	wantSnapshot(t, ev.Snapshot, "Milk", e) // as it was before deletion

	// Retrying after success: 204 and no second event.
	resp, b = f.del(t, f.alex, f.home, e.ID, eventID)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	if n := f.events(t, e.ID); n != 3 {
		t.Errorf("%d events, want 3 (created, level, deleted)", n)
	}
	// The same entry with an unused eventId, and an entry that never existed: 404.
	for _, id := range []string{e.ID, newID()} {
		resp, b = f.del(t, f.alex, f.home, id, newID())
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	}
	// A retried update of the deleted entry is 404 too.
	resp, b = f.patch(t, f.sam, f.home, e.ID, map[string]any{"eventId": newID(), "level": "low"})
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)

	// eventId validation.
	e2, _ := f.createEntry(t, f.milk, nil)
	resp, b = f.del(t, f.sam, f.home, e2.ID, uuid.New().String())
	wantError(t, resp, b, http.StatusBadRequest, CodeInvalidID)
	resp, b = f.req(t, http.MethodDelete, entryPath(f.home, e2.ID), f.sam, "")
	wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1`, e2.ID); n != 1 {
		t.Errorf("entry deleted by an invalid request")
	}
}

func TestEntryRetries(t *testing.T) {
	f := newEntryFixture(t)

	// Create retry: 200 with the existing entry, no second entry or event.
	id, eventID := newID(), newID()
	body := map[string]any{"id": id, "eventId": eventID, "catalogItemId": f.milk, "level": "half"}
	resp, b := f.post(t, f.sam, f.home, body)
	var first pantryEntry
	wantStatus(t, resp, b, http.StatusCreated, &first)
	f.update(t, f.alex, id, map[string]any{"eventId": newID(), "level": "low"})
	resp, b = f.post(t, f.sam, f.home, body)
	var again pantryEntry
	wantStatus(t, resp, b, http.StatusOK, &again)
	if again.ID != id || again.Level != "low" {
		t.Errorf("create retry = %+v, want the existing entry as it is now (level low)", again)
	}
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1`, id); n != 1 {
		t.Errorf("%d entries, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM item_events WHERE entry_id = $1 AND type = 'entry_created'`, id); n != 1 {
		t.Errorf("%d entry_created events, want 1", n)
	}

	// Update retry: 200 with the current entry, not applied again.
	e, _ := f.createEntry(t, f.milk, nil)
	upd := newID()
	f.update(t, f.sam, e.ID, map[string]any{"eventId": upd, "level": "low"})
	f.update(t, f.alex, e.ID, map[string]any{"eventId": newID(), "level": "half"})
	got := f.update(t, f.sam, e.ID, map[string]any{"eventId": upd, "level": "low"})
	if got.Level != "half" {
		t.Errorf("update retry = level %s, want half (the current level; the retry is not applied)", got.Level)
	}
	if n := f.events(t, e.ID); n != 3 {
		t.Errorf("%d events after an update retry, want 3", n)
	}

	// An eventId already used for a different entry: 409 for every endpoint.
	other, otherEvent := f.createEntry(t, f.salt, nil)
	resp, b = f.post(t, f.sam, f.home, map[string]any{"id": newID(), "eventId": otherEvent, "catalogItemId": f.salt})
	wantError(t, resp, b, http.StatusConflict, CodeEventIDConflict)
	resp, b = f.patch(t, f.sam, f.home, e.ID, map[string]any{"eventId": otherEvent, "level": "out"})
	wantError(t, resp, b, http.StatusConflict, CodeEventIDConflict)
	resp, b = f.del(t, f.sam, f.home, e.ID, otherEvent)
	wantError(t, resp, b, http.StatusConflict, CodeEventIDConflict)
	// The same entry but another kind of event is a different change too.
	resp, b = f.patch(t, f.sam, f.home, e.ID, map[string]any{"eventId": upd, "locationId": 5})
	wantError(t, resp, b, http.StatusConflict, CodeEventIDConflict)
	resp, b = f.del(t, f.sam, f.home, e.ID, upd)
	wantError(t, resp, b, http.StatusConflict, CodeEventIDConflict)
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1 AND level = 'half' AND location_id = 2`, e.ID); n != 1 {
		t.Errorf("entry changed by conflicting requests")
	}

	// An entry id that exists for a different item: 409.
	resp, b = f.post(t, f.sam, f.home, map[string]any{"id": other.ID, "eventId": newID(), "catalogItemId": f.milk})
	wantError(t, resp, b, http.StatusConflict, CodeEntryIDConflict)
	if n := f.count(t, `SELECT count(*) FROM item_events`); n != 6 {
		t.Errorf("%d events, want 6", n)
	}

	// A create retry after the entry was deleted cannot bring it back.
	resp, b = f.del(t, f.sam, f.home, first.ID, newID())
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	resp, b = f.post(t, f.sam, f.home, body)
	wantError(t, resp, b, http.StatusConflict, CodeEntryIDConflict)
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1`, id); n != 0 {
		t.Errorf("a create retry re-created a deleted entry")
	}
}

// Entry and event IDs are global, and row-level security hides another
// household's rows: a conflict with one of them must fail, never succeed
// silently.
func TestEntryIDsOfAnotherHousehold(t *testing.T) {
	f := newEntryFixture(t)
	bob := f.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	bobHome := f.create(t, bob, "Bob's").ID
	resp, b := f.post(t, bob, bobHome, map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk})
	var bobEntry pantryEntry
	wantStatus(t, resp, b, http.StatusCreated, &bobEntry)
	bobEvent := newID()
	f.patch(t, bob, bobHome, bobEntry.ID, map[string]any{"eventId": bobEvent, "level": "low"})

	snapshot := func() string {
		var s string
		if err := f.pool.QueryRow(context.Background(), `
			SELECT (SELECT string_agg(e::text, ',' ORDER BY id) FROM pantry_entries e)
			    || '|' || (SELECT string_agg(v::text, ',' ORDER BY id) FROM item_events v)`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	e, _ := f.createEntry(t, f.milk, nil)
	before := snapshot()

	resp, b = f.post(t, f.sam, f.home, map[string]any{"id": newID(), "eventId": bobEvent, "catalogItemId": f.milk})
	wantError(t, resp, b, http.StatusConflict, CodeEventIDConflict)
	resp, b = f.patch(t, f.sam, f.home, e.ID, map[string]any{"eventId": bobEvent, "level": "out"})
	wantError(t, resp, b, http.StatusConflict, CodeEventIDConflict)
	resp, b = f.del(t, f.sam, f.home, e.ID, bobEvent)
	wantError(t, resp, b, http.StatusConflict, CodeEventIDConflict)
	resp, b = f.post(t, f.sam, f.home, map[string]any{"id": bobEntry.ID, "eventId": newID(), "catalogItemId": f.milk})
	wantError(t, resp, b, http.StatusConflict, CodeEntryIDConflict)

	if after := snapshot(); after != before {
		t.Errorf("rows changed by requests with Bob's IDs:\nbefore %s\nafter  %s", before, after)
	}
}

func TestEntryAccess(t *testing.T) {
	f := newEntryFixture(t)
	bob := f.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	bobHome := f.create(t, bob, "Bob's").ID
	e, _ := f.createEntry(t, f.milk, nil)
	missing := newID()

	for _, household := range []string{f.home, missing} {
		resp, b := f.post(t, bob, household, map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk})
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
		resp, b = f.patch(t, bob, household, e.ID, map[string]any{"eventId": newID(), "level": "out"})
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
		resp, b = f.del(t, bob, household, e.ID, newID())
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	}
	// Sam's entry through Bob's own household: 404.
	resp, b := f.patch(t, bob, bobHome, e.ID, map[string]any{"eventId": newID(), "level": "out"})
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	resp, b = f.del(t, bob, bobHome, e.ID, newID())
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	// And for a member of both households, an entry ID under the wrong household.
	f.exec(t, `INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'member')`, bobHome, f.samID)
	resp, b = f.patch(t, f.sam, bobHome, e.ID, map[string]any{"eventId": newID(), "level": "out"})
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	resp, b = f.del(t, f.sam, bobHome, e.ID, newID())
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)

	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1 AND level = 'full'`, e.ID); n != 1 {
		t.Errorf("entry changed by requests that got 404")
	}
	if n := f.count(t, `SELECT count(*) FROM item_events`); n != 1 {
		t.Errorf("%d events, want 1", n)
	}
}

// The writes run as shelfkin_app with app.user_id set: a trigger records the
// role and setting of every pantry_entries and item_events write.
func TestEntryWritesRunAsAppRole(t *testing.T) {
	f := newEntryFixture(t)
	f.exec(t, `CREATE TABLE write_log (tbl text, role text, user_id text)`)
	f.exec(t, `GRANT INSERT ON write_log TO shelfkin_app`)
	f.exec(t, `CREATE FUNCTION log_write() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			INSERT INTO write_log VALUES (TG_TABLE_NAME, current_user, current_setting('app.user_id', true));
			RETURN NULL;
		END $$`)
	f.exec(t, `CREATE TRIGGER log_entries AFTER INSERT OR UPDATE OR DELETE ON pantry_entries FOR EACH ROW EXECUTE FUNCTION log_write()`)
	f.exec(t, `CREATE TRIGGER log_events AFTER INSERT ON item_events FOR EACH ROW EXECUTE FUNCTION log_write()`)

	e, _ := f.createEntry(t, f.milk, nil)
	f.update(t, f.sam, e.ID, map[string]any{"eventId": newID(), "level": "low"})
	resp, b := f.del(t, f.sam, f.home, e.ID, newID())
	wantStatus(t, resp, b, http.StatusNoContent, nil)

	if n := f.count(t, `SELECT count(*) FROM write_log`); n != 6 {
		t.Errorf("%d logged writes, want 6", n)
	}
	if n := f.count(t, `SELECT count(*) FROM write_log WHERE role <> 'shelfkin_app' OR user_id IS DISTINCT FROM $1`, f.samID); n != 0 {
		t.Errorf("%d writes not as shelfkin_app with app.user_id = caller", n)
	}
}

// When the event insert fails, the entry change is rolled back.
func TestEntryEventFailureRollsBack(t *testing.T) {
	f := newEntryFixture(t)
	e, _ := f.createEntry(t, f.milk, nil)
	f.exec(t, `CREATE FUNCTION fail_event() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'event insert failed'; END $$`)
	f.exec(t, `CREATE TRIGGER fail_event BEFORE INSERT ON item_events FOR EACH ROW EXECUTE FUNCTION fail_event()`)

	newEntry := newID()
	resp, b := f.post(t, f.sam, f.home, map[string]any{"id": newEntry, "eventId": newID(), "catalogItemId": f.milk})
	wantError(t, resp, b, http.StatusInternalServerError, CodeInternal)
	resp, b = f.patch(t, f.sam, f.home, e.ID, map[string]any{"eventId": newID(), "level": "low"})
	wantError(t, resp, b, http.StatusInternalServerError, CodeInternal)
	resp, b = f.del(t, f.sam, f.home, e.ID, newID())
	wantError(t, resp, b, http.StatusInternalServerError, CodeInternal)

	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1`, newEntry); n != 0 {
		t.Errorf("entry created without its event")
	}
	var level string
	var updatedAt time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT level::text, updated_at FROM pantry_entries WHERE id = $1`, e.ID).Scan(&level, &updatedAt); err != nil {
		t.Fatalf("entry deleted without its event: %v", err)
	}
	if level != "full" || !updatedAt.UTC().Truncate(time.Microsecond).Equal(e.UpdatedAt) {
		t.Errorf("entry changed without its event: level %s, updated_at %v", level, updatedAt)
	}
}

// Members changing the same entry at the same time all succeed; the changes
// apply one after the other, each event's "from" is the previous "to", and
// the entry ends at the last one.
func TestConcurrentEntryUpdates(t *testing.T) {
	f := newEntryFixture(t)
	e, _ := f.createEntry(t, f.milk, nil)
	levels := []string{"half", "low", "out", "full", "half", "low", "out", "half"}
	var wg sync.WaitGroup
	errs := make(chan string, len(levels))
	for i, level := range levels {
		wg.Add(1)
		token := f.sam
		if i%2 == 1 {
			token = f.alex
		}
		go func() {
			defer wg.Done()
			resp, b := f.patch(t, token, f.home, e.ID, map[string]any{"eventId": newID(), "level": level})
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Sprintf("level %s: %d %s", level, resp.StatusCode, b)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}

	rows, err := f.pool.Query(context.Background(), `
		SELECT changes->'level'->>'from', changes->'level'->>'to'
		FROM item_events WHERE entry_id = $1 AND type = 'level_changed'
		ORDER BY occurred_at, id`, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var chain [][2]string
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			t.Fatal(err)
		}
		chain = append(chain, [2]string{from, to})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// Some updates may be no-ops (the level was already set by another one);
	// every real change is recorded and the chain is unbroken.
	cur := "full"
	for _, c := range chain {
		if c[0] != cur || c[0] == c[1] {
			t.Errorf("event chain %v is broken at %v", chain, c)
			break
		}
		cur = c[1]
	}
	if len(chain) < len(levels)/2 {
		t.Errorf("only %d of %d concurrent updates recorded: %v", len(chain), len(levels), chain)
	}
	var final string
	if err := f.pool.QueryRow(context.Background(), `SELECT level::text FROM pantry_entries WHERE id = $1`, e.ID).Scan(&final); err != nil {
		t.Fatal(err)
	}
	if final != cur {
		t.Errorf("final level %s, want %s (the last event's to)", final, cur)
	}
}
