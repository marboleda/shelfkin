package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/expiry"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// fakeAPI's pantry read endpoint, so it keeps satisfying StrictServerInterface.
func (fakeAPI) GetPantry(context.Context, openapi.GetPantryRequestObject) (openapi.GetPantryResponseObject, error) {
	return nil, errNotImplemented
}

// Category and location IDs from the seed (20261029115346_categories_locations.sql).
const (
	categoryProduce = 1
	categoryDairy   = 2
	categorySpices  = 6
	locationPantry  = 1
	locationFridge  = 2
	locationFreezer = 3
	locationSpices  = 4
)

// pantryResp is a decoded Pantry response.
type pantryResp struct {
	Today  string      `json:"today"`
	Groups []pantryGrp `json:"groups"`
}

type pantryGrp struct {
	CatalogItemID string         `json:"catalogItemId"`
	Name          string         `json:"name"`
	IsCustom      bool           `json:"isCustom"`
	CategoryID    int            `json:"categoryId"`
	Entries       []pantryEntryX `json:"entries"`
}

// pantryEntryX is a decoded PantryListEntry.
type pantryEntryX struct {
	pantryEntry
	ExpiryStatus *string     `json:"expiryStatus"`
	LastChange   *lastChange `json:"lastChange"`
}

type lastChange struct {
	Type       string                    `json:"type"`
	Changes    map[string]map[string]any `json:"changes"`
	ActorID    *string                   `json:"actorId"`
	ActorName  *string                   `json:"actorName"`
	OccurredAt time.Time                 `json:"occurredAt"`
}

func pantryPath(household string) string { return "/households/" + household + "/pantry" }

// pantry gets the pantry of household as token, with the query string query
// (without "?"), and returns the 200 response.
func (f *entryFixture) pantry(t *testing.T, token, household, query string) pantryResp {
	t.Helper()
	path := pantryPath(household)
	if query != "" {
		path += "?" + query
	}
	resp, b := f.req(t, http.MethodGet, path, token, "")
	var p pantryResp
	wantStatus(t, resp, b, http.StatusOK, &p)
	return p
}

// summary is the groups as "Name:entryID,entryID" strings, for comparisons.
func (p pantryResp) summary() []string {
	out := []string{}
	for _, g := range p.Groups {
		ids := []string{}
		for _, e := range g.Entries {
			ids = append(ids, e.ID)
		}
		out = append(out, g.Name+":"+strings.Join(ids, ","))
	}
	return out
}

func group(name string, entries ...pantryEntry) string {
	ids := []string{}
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return name + ":" + strings.Join(ids, ",")
}

func wantGroups(t *testing.T, label string, p pantryResp, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := p.summary(); !slices.Equal(got, want) {
		t.Errorf("%s: groups = %v, want %v", label, got, want)
	}
}

// entryByID finds an entry anywhere in the pantry.
func (p pantryResp) entry(t *testing.T, id string) pantryEntryX {
	t.Helper()
	for _, g := range p.Groups {
		for _, e := range g.Entries {
			if e.ID == id {
				return e
			}
		}
	}
	t.Fatalf("entry %s not in the pantry", id)
	return pantryEntryX{}
}

func (p pantryResp) group(t *testing.T, name string) pantryGrp {
	t.Helper()
	for _, g := range p.Groups {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("no group %s in %v", name, p.summary())
	return pantryGrp{}
}

func TestPantryGrouping(t *testing.T) {
	f := newEntryFixture(t)

	// Empty pantry: groups is [] (not null), and today is Sam's date (UTC).
	resp, b := f.req(t, http.MethodGet, pantryPath(f.home), f.sam, "")
	var empty map[string]json.RawMessage
	wantStatus(t, resp, b, http.StatusOK, &empty)
	if len(empty) != 2 || string(empty["groups"]) != "[]" || string(empty["today"]) != `"2026-11-05"` {
		t.Errorf("empty pantry = %s, want groups [] and today 2026-11-05", b)
	}

	// Two milks (created newest purchase first), one salt, one custom item
	// whose lower-case name sorts first, and an out entry.
	milkNew, _ := f.createEntry(t, f.milk, map[string]any{"purchasedOn": "2026-11-03"})
	milkOld, _ := f.createEntry(t, f.milk, map[string]any{"purchasedOn": "2026-11-01"})
	milkSameDay, _ := f.createEntry(t, f.milk, map[string]any{"purchasedOn": "2026-11-03", "level": "out"})
	salt, _ := f.createEntry(t, f.salt, nil)
	apples := f.customItem(t, f.home, "apples")
	apple, _ := f.createEntry(t, apples, nil)

	p := f.pantry(t, f.sam, f.home, "")
	if p.Today != "2026-11-05" {
		t.Errorf("today = %s, want 2026-11-05", p.Today)
	}
	// Groups by name ignoring case; entries by purchasedOn, then createdAt.
	// Items with no entries (Bread, Butter, ...) do not appear.
	wantGroups(t, "pantry", p,
		group("apples", apple),
		group("Milk", milkOld, milkNew, milkSameDay),
		group("Salt", salt))

	milkGroup := p.group(t, "Milk")
	if milkGroup.CatalogItemID != f.milk || milkGroup.IsCustom || milkGroup.CategoryID != categoryDairy {
		t.Errorf("Milk group = %+v, want built-in, dairy", milkGroup)
	}
	appleGroup := p.group(t, "apples")
	if appleGroup.CatalogItemID != apples || !appleGroup.IsCustom || appleGroup.CategoryID != categoryProduce {
		t.Errorf("apples group = %+v, want custom, produce", appleGroup)
	}
	if g := p.group(t, "Salt"); g.CategoryID != categorySpices {
		t.Errorf("Salt category = %d, want %d", g.CategoryID, categorySpices)
	}

	// Each entry is a PantryEntry with the same values as the write
	// endpoints return, plus expiryStatus and lastChange.
	got := p.entry(t, milkOld.ID)
	if got.CatalogItemID != f.milk || got.LocationID != milkOld.LocationID || got.Level != milkOld.Level ||
		got.PurchasedOn != milkOld.PurchasedOn || !eqStrp(got.ExpiresOn, milkOld.ExpiresOn) ||
		!got.CreatedAt.Equal(milkOld.CreatedAt) || !got.UpdatedAt.Equal(milkOld.UpdatedAt) {
		t.Errorf("entry = %+v, want %+v", got.pantryEntry, milkOld)
	}
	if p.entry(t, milkSameDay.ID).Level != "out" {
		t.Errorf("out entry missing or wrong level")
	}
	var raw struct {
		Groups []struct {
			Entries []map[string]json.RawMessage `json:"entries"`
		} `json:"groups"`
	}
	_, b = f.req(t, http.MethodGet, pantryPath(f.home), f.sam, "")
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	want := []string{"catalogItemId", "createdAt", "expiresOn", "expiryStatus", "id", "lastChange",
		"level", "locationId", "purchasedOn", "updatedAt"}
	for _, g := range raw.Groups {
		for _, e := range g.Entries {
			keys := []string{}
			for k := range e {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if !slices.Equal(keys, want) {
				t.Errorf("entry keys = %v, want %v", keys, want)
			}
		}
	}
}

func TestPantryCategoryOverride(t *testing.T) {
	f := newEntryFixture(t)
	bob := f.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	bobHome := f.create(t, bob, "Bob's").ID
	f.createEntry(t, f.milk, nil)
	resp, b := f.post(t, bob, bobHome, map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk})
	wantStatus(t, resp, b, http.StatusCreated, nil)

	// Home files Milk under Other (12). #18's endpoint is not on main yet, so
	// the override is written directly.
	f.exec(t, `INSERT INTO household_item_defaults (household_id, catalog_item_id, category_id) VALUES ($1, $2, 12)`, f.home, f.milk)

	if g := f.pantry(t, f.sam, f.home, "").group(t, "Milk"); g.CategoryID != 12 {
		t.Errorf("home's Milk category = %d, want 12 (its override)", g.CategoryID)
	}
	if g := f.pantry(t, bob, bobHome, "").group(t, "Milk"); g.CategoryID != categoryDairy {
		t.Errorf("Bob's Milk category = %d, want %d (no override)", g.CategoryID, categoryDairy)
	}
	// The category filter uses the effective category.
	if n := len(f.pantry(t, f.sam, f.home, "categoryId=12").Groups); n != 1 {
		t.Errorf("home, categoryId=12: %d groups, want 1", n)
	}
	if n := len(f.pantry(t, f.sam, f.home, "categoryId=2").Groups); n != 0 {
		t.Errorf("home, categoryId=2: %d groups, want 0", n)
	}
	if n := len(f.pantry(t, bob, bobHome, "categoryId=2").Groups); n != 1 {
		t.Errorf("Bob, categoryId=2: %d groups, want 1", n)
	}
}

func TestExpiryStatus(t *testing.T) {
	// One window for the pantry and the expiry push (#22): 3 days.
	if got := [2]int{ExpirySoonDays, expiry.SoonDays}; got != [2]int{3, 3} {
		t.Errorf("ExpirySoonDays, expiry.SoonDays = %v, want 3 and 3", got)
	}
	d := func(s string) pgtype.Date {
		tm, err := time.Parse(time.DateOnly, s)
		if err != nil {
			t.Fatal(err)
		}
		return pgtype.Date{Time: tm, Valid: true}
	}
	today := d("2026-12-30")
	tests := []struct {
		level   db.StockLevel
		expires pgtype.Date
		want    string
	}{
		{db.StockLevelFull, d("2026-12-29"), "expired"},
		{db.StockLevelFull, d("2025-01-01"), "expired"},
		{db.StockLevelLow, d("2026-12-30"), "soon"},
		{db.StockLevelHalf, d("2027-01-02"), "soon"}, // across the year end
		{db.StockLevelFull, d("2027-01-03"), ""},
		{db.StockLevelFull, pgtype.Date{}, ""},
		{db.StockLevelOut, d("2026-12-31"), ""},
		{db.StockLevelOut, d("2026-12-01"), ""},
	}
	for _, tt := range tests {
		got := ""
		if s := ExpiryStatus(tt.level, tt.expires, today); s != nil {
			got = string(*s)
		}
		if got != tt.want {
			t.Errorf("ExpiryStatus(%s, %v) = %q, want %q", tt.level, tt.expires.Time.Format(time.DateOnly), got, tt.want)
		}
	}
}

func TestPantryExpiryStatus(t *testing.T) {
	f := newEntryFixture(t)
	// fixedNow is 2026-11-05 in UTC (Sam's timezone).
	mk := func(expires any, level string) pantryEntry {
		e, _ := f.createEntry(t, f.salt, map[string]any{"purchasedOn": "2026-10-01", "expiresOn": expires, "level": level})
		return e
	}
	yesterday := mk("2026-11-04", "full")
	today := mk("2026-11-05", "low")
	plus3 := mk("2026-11-08", "half")
	plus4 := mk("2026-11-09", "full")
	noExpiry := mk(nil, "full")
	outSoon := mk("2026-11-06", "out")
	outExpired := mk("2026-11-01", "out")

	check := func(label string, p pantryResp, want map[pantryEntry]string) {
		t.Helper()
		for e, w := range want {
			got := p.entry(t, e.ID).ExpiryStatus
			if fmtStrp(got) != w {
				t.Errorf("%s: entry expiring %s (%s): expiryStatus = %s, want %s",
					label, fmtStrp(e.ExpiresOn), e.Level, fmtStrp(got), w)
			}
		}
	}
	p := f.pantry(t, f.sam, f.home, "")
	if p.Today != "2026-11-05" {
		t.Errorf("UTC: today = %s, want 2026-11-05", p.Today)
	}
	check("UTC", p, map[pantryEntry]string{
		yesterday: "expired", today: "soon", plus3: "soon", plus4: "null",
		noExpiry: "null", outSoon: "null", outExpired: "null",
	})

	// In Toronto it is still 2026-11-04 (21:00), so every date shifts by one.
	f.user(t, http.MethodPatch, f.sam, `{"timezone":"America/Toronto"}`)
	p = f.pantry(t, f.sam, f.home, "")
	if p.Today != "2026-11-04" {
		t.Errorf("Toronto: today = %s, want 2026-11-04", p.Today)
	}
	check("Toronto", p, map[pantryEntry]string{
		yesterday: "soon", today: "soon", plus3: "null", plus4: "null",
		noExpiry: "null", outSoon: "null", outExpired: "null",
	})
	// Alex (UTC) sees the same entries against his own date.
	if got := f.pantry(t, f.alex, f.home, "").entry(t, yesterday.ID).ExpiryStatus; fmtStrp(got) != "expired" {
		t.Errorf("Alex (UTC): yesterday's entry = %s, want expired", fmtStrp(got))
	}
}

func TestPantryLastChange(t *testing.T) {
	f := newEntryFixture(t)
	e, createdEvent := f.createEntry(t, f.milk, nil)

	p := f.pantry(t, f.sam, f.home, "")
	lc := p.entry(t, e.ID).LastChange
	created := f.event(t, createdEvent)
	if lc == nil || lc.Type != "entry_created" || len(lc.Changes) != 0 || fmtStrp(lc.ActorID) != f.samID ||
		fmtStrp(lc.ActorName) != "Sam" || !lc.OccurredAt.Equal(created.OccurredAt) {
		t.Errorf("after create: lastChange = %+v, want entry_created by Sam at %v", lc, created.OccurredAt)
	}

	// Alex sets it to Low: the label is the level change by Alex.
	levelEvent := newID()
	f.update(t, f.alex, e.ID, map[string]any{"eventId": levelEvent, "level": "low"})
	changed := f.event(t, levelEvent)
	lc = f.pantry(t, f.sam, f.home, "").entry(t, e.ID).LastChange
	if lc == nil || lc.Type != "level_changed" || fmtStrp(lc.ActorID) != f.alexID || fmtStrp(lc.ActorName) != "Alex" ||
		!lc.OccurredAt.Equal(changed.OccurredAt) || lc.OccurredAt.Before(created.OccurredAt) {
		t.Fatalf("after level change: lastChange = %+v, want level_changed by Alex at %v", lc, changed.OccurredAt)
	}
	if c := lc.Changes["level"]; len(lc.Changes) != 1 || c["from"] != "full" || c["to"] != "low" {
		t.Errorf("changes = %v, want {level: {from: full, to: low}}", lc.Changes)
	}

	// An expiry change to null keeps the null in changes.
	f.update(t, f.sam, e.ID, map[string]any{"eventId": newID(), "expiresOn": nil})
	lc = f.pantry(t, f.sam, f.home, "").entry(t, e.ID).LastChange
	if c, ok := lc.Changes["expires_on"]; lc.Type != "expiry_changed" || !ok || c["from"] != *e.ExpiresOn || c["to"] != nil {
		t.Errorf("after clearing expiry: lastChange = %+v", lc)
	}
	f.update(t, f.alex, e.ID, map[string]any{"eventId": newID(), "locationId": locationFreezer})
	lc = f.pantry(t, f.sam, f.home, "").entry(t, e.ID).LastChange
	if c := lc.Changes["location_id"]; lc.Type != "location_changed" || c["from"] != float64(locationFridge) || c["to"] != float64(locationFreezer) {
		t.Errorf("after location change: lastChange = %+v", lc)
	}

	// The name is the actor's current display name.
	f.user(t, http.MethodPatch, f.alex, `{"displayName":"Alexandra"}`)
	if lc = f.pantry(t, f.sam, f.home, "").entry(t, e.ID).LastChange; fmtStrp(lc.ActorName) != "Alexandra" {
		t.Errorf("after rename: actorName = %s, want Alexandra", fmtStrp(lc.ActorName))
	}

	// Alex leaves the household: still named.
	f.exec(t, `DELETE FROM household_members WHERE household_id = $1 AND user_id = $2`, f.home, f.alexID)
	if lc = f.pantry(t, f.sam, f.home, "").entry(t, e.ID).LastChange; fmtStrp(lc.ActorID) != f.alexID || fmtStrp(lc.ActorName) != "Alexandra" {
		t.Errorf("after Alex left: actor = %s / %s, want Alex's ID and Alexandra", fmtStrp(lc.ActorID), fmtStrp(lc.ActorName))
	}

	// Alex's account is deleted: actor_id becomes NULL, and so does the name.
	f.exec(t, `DELETE FROM users WHERE id = $1`, f.alexID)
	lc = f.pantry(t, f.sam, f.home, "").entry(t, e.ID).LastChange
	if lc == nil || lc.Type != "location_changed" || lc.ActorID != nil || lc.ActorName != nil {
		t.Errorf("after Alex was deleted: lastChange = %+v, want location_changed with null actorId and actorName", lc)
	}

	// An entry written outside the API, with no events: lastChange is null.
	bare := newID()
	f.exec(t, `INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level, purchased_on)
		VALUES ($1, $2, $3, 1, 'full', '2026-11-01')`, bare, f.home, f.salt)
	resp, b := f.req(t, http.MethodGet, pantryPath(f.home), f.sam, "")
	var p2 pantryResp
	wantStatus(t, resp, b, http.StatusOK, &p2)
	if got := p2.entry(t, bare); got.LastChange != nil {
		t.Errorf("entry with no events: lastChange = %+v, want null", got.LastChange)
	}
	if !strings.Contains(string(b), `"lastChange":null`) {
		t.Errorf("body %s: want an explicit \"lastChange\":null", b)
	}
}

func TestPantryFilters(t *testing.T) {
	f := newEntryFixture(t)
	fridgeMilk, _ := f.createEntry(t, f.milk, map[string]any{"locationId": locationFridge, "purchasedOn": "2026-11-01"})
	freezerMilk, _ := f.createEntry(t, f.milk, map[string]any{"locationId": locationFreezer, "purchasedOn": "2026-11-02"})
	salt, _ := f.createEntry(t, f.salt, nil) // spice rack, spices
	jam := f.customItem(t, f.home, "Jam")    // pantry, produce
	jamEntry, _ := f.createEntry(t, jam, nil)

	tests := []struct {
		query string
		want  []string
	}{
		{"", []string{group("Jam", jamEntry), group("Milk", fridgeMilk, freezerMilk), group("Salt", salt)}},
		// "Milk ×2" shows as "Milk ×1" under a Fridge filter.
		{"locationId=2", []string{group("Milk", fridgeMilk)}},
		{"locationId=2&locationId=4", []string{group("Milk", fridgeMilk), group("Salt", salt)}},
		{"locationId=2&locationId=2", []string{group("Milk", fridgeMilk)}},
		{"locationId=5", nil},
		{"categoryId=2", []string{group("Milk", fridgeMilk, freezerMilk)}},
		{"categoryId=2&categoryId=1", []string{group("Jam", jamEntry), group("Milk", fridgeMilk, freezerMilk)}},
		{"categoryId=3", nil},
		{"locationId=3&categoryId=2", []string{group("Milk", freezerMilk)}},
		{"locationId=1&locationId=3&categoryId=2&categoryId=6", []string{group("Milk", freezerMilk)}},
		{"locationId=4&categoryId=2", nil},
	}
	for _, tt := range tests {
		wantGroups(t, "?"+tt.query, f.pantry(t, f.sam, f.home, tt.query), tt.want...)
	}

	invalid := []string{
		"locationId=abc", "locationId=99", "locationId=0", "locationId=-1", "locationId=", "locationId=2.0",
		"locationId=70000", "locationId=2&locationId=x", "categoryId=13", "categoryId=1.5", "categoryId=dairy",
		"categoryId=2&locationId=99", "locationId=2&categoryId=99",
	}
	for _, q := range invalid {
		resp, b := f.req(t, http.MethodGet, pantryPath(f.home)+"?"+q, f.sam, "")
		wantError(t, resp, b, http.StatusBadRequest, CodeInvalidFilter)
	}
}

func TestPantryAccess(t *testing.T) {
	f := newEntryFixture(t)
	bob := f.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	bobHome := f.create(t, bob, "Bob's").ID
	mine, _ := f.createEntry(t, f.milk, nil)
	resp, b := f.post(t, bob, bobHome, map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk})
	var bobs pantryEntry
	wantStatus(t, resp, b, http.StatusCreated, &bobs)
	bobsItem := f.customItem(t, bobHome, "Bob's sauce")
	resp, b = f.post(t, bob, bobHome, map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": bobsItem})
	var sauce pantryEntry
	wantStatus(t, resp, b, http.StatusCreated, &sauce)

	// Household B's entries never appear in A's pantry, filtered or not.
	for _, q := range []string{"", "locationId=2", "categoryId=2", "categoryId=1"} {
		p := f.pantry(t, f.sam, f.home, q)
		for _, g := range p.Groups {
			for _, e := range g.Entries {
				if e.ID != mine.ID {
					t.Errorf("?%s: home's pantry has entry %s (%s), want only %s", q, e.ID, g.Name, mine.ID)
				}
			}
		}
	}
	wantGroups(t, "Bob's pantry", f.pantry(t, bob, bobHome, ""), group("Bob's sauce", sauce), group("Milk", bobs))

	// A non-member, and a household that does not exist, get 404, also with
	// a filter.
	resp, b = f.req(t, http.MethodGet, pantryPath(f.home), bob, "")
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	resp, b = f.req(t, http.MethodGet, pantryPath(f.home)+"?locationId=2", bob, "")
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	resp, b = f.req(t, http.MethodGet, pantryPath(newID()), f.sam, "")
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	// A member who left loses access.
	f.exec(t, `DELETE FROM household_members WHERE household_id = $1 AND user_id = $2`, f.home, f.alexID)
	resp, b = f.req(t, http.MethodGet, pantryPath(f.home), f.alex, "")
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)

	resp, b = f.req(t, http.MethodGet, pantryPath("not-a-uuid"), f.sam, "")
	wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)
	resp, b = f.req(t, http.MethodGet, pantryPath(f.home), "", "")
	wantError(t, resp, b, http.StatusUnauthorized, "unauthorized")
}

// TestPantryQueriesAndLatency checks that the pantry is built from the same
// number of SQL queries for 1 entry as for 500 entries over 200 items, and
// measures the latency at that size. Timing depends on the machine, so the
// 200 ms limit is enforced only with SHELFKIN_PERF=1:
//
//	SHELFKIN_PERF=1 go test ./internal/server -run TestPantryQueriesAndLatency -v -count=1
func TestPantryQueriesAndLatency(t *testing.T) {
	f := newEntryFixture(t)
	ctx := context.Background()

	// The API under test runs on its own pool that counts queries; the
	// authenticator keeps using the fixture's pool.
	cfg := f.pool.Config()
	counter := &queryCounter{}
	cfg.ConnConfig.Tracer = counter
	counted, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(counted.Close)
	logger, _ := newLogger()
	api := NewAPI(counted, logger)
	api.now = func() time.Time { return fixedNow }
	url := serve(t, NewHandler(api, auth.NewAuthenticator(f.verifier, f.pool), logger, nil))
	s := &meServer{url: url, pool: f.pool, verifier: f.verifier}

	get := func(query string) (pantryResp, int, time.Duration) {
		t.Helper()
		path := pantryPath(f.home)
		if query != "" {
			path += "?" + query
		}
		counter.reset()
		start := time.Now()
		resp, b := s.req(t, http.MethodGet, path, f.sam, "")
		d := time.Since(start)
		var p pantryResp
		wantStatus(t, resp, b, http.StatusOK, &p)
		return p, len(counter.reset()), d
	}

	f.createEntry(t, f.milk, nil)
	_, small, _ := get("")
	_, smallFiltered, _ := get("locationId=2&categoryId=2")

	// 200 custom items and 500 entries (2 or 3 per item), each with a create
	// and a level change event.
	f.exec(t, `INSERT INTO catalog_items (household_id, name, default_category_id, default_location_id)
		SELECT $1, 'Item ' || g, 1 + g % 12, 1 + g % 5 FROM generate_series(1, 200) g`, f.home)
	f.exec(t, `
		WITH items AS (
			SELECT id, row_number() OVER (ORDER BY id) - 1 AS n FROM catalog_items WHERE household_id = $1
		), entries AS (
			INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level, purchased_on, expires_on)
			SELECT uuidv7(), $1, i.id, 1 + g % 5, 'full', DATE '2026-10-01' + g % 30, DATE '2026-11-01' + g % 10
			FROM generate_series(1, 499) g JOIN items i ON i.n = g % 200
			RETURNING id, catalog_item_id
		)
		INSERT INTO item_events (id, household_id, entry_id, catalog_item_id, actor_id, type, changes, snapshot, occurred_at)
		SELECT uuidv7(), $1, e.id, e.catalog_item_id, $2, t.type::event_type, '{}', '{}', now() - t.ago
		FROM entries e
		CROSS JOIN (VALUES ('entry_created', interval '2 hours'), ('level_changed', interval '1 hour')) t(type, ago)`,
		f.home, f.samID)
	f.exec(t, `VACUUM ANALYZE pantry_entries, item_events, catalog_items`)

	p, large, _ := get("")
	n := 0
	for _, g := range p.Groups {
		n += len(g.Entries)
		for _, e := range g.Entries {
			if e.LastChange == nil {
				t.Fatalf("entry %s has no lastChange", e.ID)
			}
		}
	}
	if len(p.Groups) != 201 || n != 500 {
		t.Fatalf("large pantry: %d groups, %d entries; want 201 and 500", len(p.Groups), n)
	}
	_, largeFiltered, _ := get("locationId=2&categoryId=2")
	if small != large || smallFiltered != largeFiltered {
		t.Errorf("queries: %d for 1 entry, %d for 500 (filtered: %d, %d); want the same", small, large, smallFiltered, largeFiltered)
	}
	t.Logf("queries per request: %d unfiltered, %d filtered", large, largeFiltered)

	// Latency: median of 11 requests after a warm-up.
	get("")
	times := make([]time.Duration, 11)
	for i := range times {
		_, _, times[i] = get("")
	}
	slices.Sort(times)
	median := times[len(times)/2]
	t.Logf("500 entries over 200 items: median %v, max %v", median, times[len(times)-1])
	if os.Getenv("SHELFKIN_PERF") == "1" && median >= 200*time.Millisecond {
		t.Errorf("median latency %v, want under 200ms", median)
	}
}
