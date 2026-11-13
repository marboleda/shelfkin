package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// fakeAPI's custom item endpoints, so it keeps satisfying StrictServerInterface.

func (fakeAPI) CreateCatalogItem(context.Context, openapi.CreateCatalogItemRequestObject) (openapi.CreateCatalogItemResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) GetCatalogItem(context.Context, openapi.GetCatalogItemRequestObject) (openapi.GetCatalogItemResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) UpdateCatalogItem(context.Context, openapi.UpdateCatalogItemRequestObject) (openapi.UpdateCatalogItemResponseObject, error) {
	return nil, errNotImplemented
}

func TestCleanCatalogItemName(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Gochujang", "Gochujang", true},
		{"  Gochujang  ", "Gochujang", true},
		{"Gochujang \t\n  paste", "Gochujang paste", true},
		{" a  b   c ", "a b c", true},
		{"", "", false},
		{" \t\n ", "", false},
		{strings.Repeat("a", 60), strings.Repeat("a", 60), true},
		{strings.Repeat("é", 60), strings.Repeat("é", 60), true}, // characters, not bytes
		{"  " + strings.Repeat("a", 60) + "  ", strings.Repeat("a", 60), true},
		{strings.Repeat("a", 61), "", false},
		// 30 + 1 + 29 = 60 after collapsing the run of spaces.
		{strings.Repeat("a", 30) + "      " + strings.Repeat("b", 29), strings.Repeat("a", 30) + " " + strings.Repeat("b", 29), true},
	}
	for _, tt := range tests {
		got, ok := CleanCatalogItemName(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("CleanCatalogItemName(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// itemResponse is a decoded CatalogItem with the override flags.
type itemResponse struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	IsCustom           bool   `json:"isCustom"`
	CategoryID         int    `json:"categoryId"`
	LocationID         int    `json:"locationId"`
	ShelfLifeDays      *int   `json:"shelfLifeDays"`
	CategoryOverridden bool   `json:"categoryOverridden"`
	LocationOverridden bool   `json:"locationOverridden"`
}

// itemFixture is entryFixture (Sam owns home, Alex is a member) plus Bo, who
// owns away, and the IDs of some categories and locations.
type itemFixture struct {
	*entryFixture
	bo                         string // token
	boID                       string
	away                       string // household ID
	dairy, produce, other      int    // category IDs
	fridge, freezer, pantry    int    // location IDs
	milkCategory, milkLocation int    // Milk's built-in defaults
}

func newItemFixture(t *testing.T) *itemFixture {
	t.Helper()
	f := &itemFixture{entryFixture: newEntryFixture(t)}
	f.bo = f.verifier.Issue(auth.Token{UID: "bo", Name: "Bo"})
	f.boID = f.userID(t, f.bo)
	f.away = f.create(t, f.bo, "Away").ID
	f.dairy = f.lookupID(t, "categories", "dairy")
	f.produce = f.lookupID(t, "categories", "produce")
	f.other = f.lookupID(t, "categories", "other")
	f.fridge = f.lookupID(t, "locations", "fridge")
	f.freezer = f.lookupID(t, "locations", "freezer")
	f.pantry = f.lookupID(t, "locations", "pantry")
	f.milkCategory = f.count(t, `SELECT default_category_id FROM catalog_items WHERE id = $1`, f.milk)
	f.milkLocation = f.count(t, `SELECT default_location_id FROM catalog_items WHERE id = $1`, f.milk)
	if f.milkCategory != f.dairy || f.milkLocation != f.fridge {
		t.Fatalf("Milk's built-in defaults are %d/%d, want dairy/fridge (%d/%d)", f.milkCategory, f.milkLocation, f.dairy, f.fridge)
	}
	return f
}

func itemsPath(household string) string { return "/households/" + household + "/catalog-items" }

func itemPath(household, item string) string { return itemsPath(household) + "/" + item }

func (f *itemFixture) postItem(t *testing.T, token, household string, body map[string]any) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodPost, itemsPath(household), token, jsonBody(t, body))
}

// createItem creates a custom item in household as token and returns it.
func (f *itemFixture) createItem(t *testing.T, token, household, name string, extra map[string]any) itemResponse {
	t.Helper()
	body := map[string]any{"name": name, "categoryId": f.other, "locationId": f.pantry}
	for k, v := range extra {
		body[k] = v
	}
	resp, b := f.postItem(t, token, household, body)
	var it itemResponse
	wantStatus(t, resp, b, http.StatusCreated, &it)
	return it
}

func (f *itemFixture) getItem(t *testing.T, token, household, item string) itemResponse {
	t.Helper()
	resp, b := f.req(t, http.MethodGet, itemPath(household, item), token, "")
	var it itemResponse
	wantStatus(t, resp, b, http.StatusOK, &it)
	return it
}

func (f *itemFixture) patchItem(t *testing.T, token, household, item, body string) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodPatch, itemPath(household, item), token, body)
}

// updateItem patches item in household and returns the 200 response.
func (f *itemFixture) updateItem(t *testing.T, token, household, item, body string) itemResponse {
	t.Helper()
	resp, b := f.patchItem(t, token, household, item, body)
	var it itemResponse
	wantStatus(t, resp, b, http.StatusOK, &it)
	return it
}

// overrideRows is the number of household_item_defaults rows for item in
// household, read as the superuser.
func (f *itemFixture) overrideRows(t *testing.T, household, item string) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM household_item_defaults WHERE household_id = $1 AND catalog_item_id = $2`, household, item)
}

func (f *itemFixture) itemUpdatedAt(t *testing.T, item string) time.Time {
	t.Helper()
	var at time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT updated_at FROM catalog_items WHERE id = $1`, item).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func intp(i int) *int { return &i }

func eqIntp(a, b *int) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

func wantItem(t *testing.T, got, want itemResponse) {
	t.Helper()
	if got.ID != want.ID || got.Name != want.Name || got.IsCustom != want.IsCustom ||
		got.CategoryID != want.CategoryID || got.LocationID != want.LocationID ||
		!eqIntp(got.ShelfLifeDays, want.ShelfLifeDays) ||
		got.CategoryOverridden != want.CategoryOverridden || got.LocationOverridden != want.LocationOverridden {
		t.Errorf("item = %+v (shelf life %v), want %+v (shelf life %v)", got, fmtIntp(got.ShelfLifeDays), want, fmtIntp(want.ShelfLifeDays))
	}
}

func fmtIntp(i *int) string {
	if i == nil {
		return "null"
	}
	return fmt.Sprint(*i)
}

func TestCreateCatalogItem(t *testing.T) {
	t.Parallel()
	f := newItemFixture(t)

	it := f.createItem(t, f.alex, f.home, "  Gochujang \t  paste ", map[string]any{
		"categoryId": f.produce, "locationId": f.fridge, "shelfLifeDays": 365,
	})
	want := itemResponse{ID: it.ID, Name: "Gochujang paste", IsCustom: true,
		CategoryID: f.produce, LocationID: f.fridge, ShelfLifeDays: intp(365)}
	wantItem(t, it, want)

	// Stored as cleaned, in home, created by the caller.
	var name, household, createdBy string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT name, household_id::text, created_by::text FROM catalog_items WHERE id = $1`, it.ID).
		Scan(&name, &household, &createdBy); err != nil {
		t.Fatal(err)
	}
	if name != "Gochujang paste" || household != f.home || createdBy != f.alexID {
		t.Errorf("row = %q, %s, %s; want %q, %s, %s", name, household, createdBy, "Gochujang paste", f.home, f.alexID)
	}

	wantItem(t, f.getItem(t, f.sam, f.home, it.ID), want)
	if _, ok := find(f.search(t, f.sam, f.home, "gochujang", 25), "Gochujang paste"); !ok {
		t.Error("home's search does not find the new item")
	}
	if _, ok := find(f.search(t, f.bo, f.away, "gochujang", 25), "Gochujang paste"); ok {
		t.Error("away's search finds home's custom item")
	}

	// shelfLifeDays omitted or null: no shelf life.
	noShelfLife := f.createItem(t, f.sam, f.home, "No shelf life", nil)
	wantItem(t, noShelfLife,
		itemResponse{ID: noShelfLife.ID, Name: "No shelf life", IsCustom: true, CategoryID: f.other, LocationID: f.pantry})
	got := f.createItem(t, f.sam, f.home, "Null shelf life", map[string]any{"shelfLifeDays": nil})
	if got.ShelfLifeDays != nil {
		t.Errorf("shelfLifeDays null: got %d, want null", *got.ShelfLifeDays)
	}
	// The limits are allowed.
	for _, d := range []int{1, 3650} {
		got := f.createItem(t, f.sam, f.home, fmt.Sprintf("Shelf life %d", d), map[string]any{"shelfLifeDays": d})
		if !eqIntp(got.ShelfLifeDays, &d) {
			t.Errorf("shelfLifeDays %d: got %s", d, fmtIntp(got.ShelfLifeDays))
		}
	}
	// 60 characters is fine.
	f.createItem(t, f.sam, f.home, strings.Repeat("é", 60), nil)
	// No events.
	if n := f.count(t, `SELECT count(*) FROM item_events`); n != 0 {
		t.Errorf("%d item_events rows, want 0", n)
	}
}

func TestCreateCatalogItemValidation(t *testing.T) {
	t.Parallel()
	f := newItemFixture(t)
	f.createItem(t, f.sam, f.home, "Gochujang", nil)
	f.createItem(t, f.bo, f.away, "Away only", nil)

	base := func(extra map[string]any) map[string]any {
		body := map[string]any{"name": "Fresh name", "categoryId": f.other, "locationId": f.pantry}
		for k, v := range extra {
			if v == nil {
				delete(body, k)
				continue
			}
			body[k] = v
		}
		return body
	}
	tests := []struct {
		name   string
		body   map[string]any
		status int
		code   string
	}{
		{"empty name", base(map[string]any{"name": ""}), 400, CodeInvalidName},
		{"blank name", base(map[string]any{"name": "  \t "}), 400, CodeInvalidName},
		{"missing name", base(map[string]any{"name": nil}), 400, CodeInvalidName},
		{"61 characters", base(map[string]any{"name": strings.Repeat("a", 61)}), 400, CodeInvalidName},
		{"built-in name", base(map[string]any{"name": "Milk"}), 409, CodeNameTaken},
		{"built-in name, other case and spaces", base(map[string]any{"name": "  mILK "}), 409, CodeNameTaken},
		{"built-in name with collapsed whitespace", base(map[string]any{"name": "Oat   milk"}), 409, CodeNameTaken},
		{"own custom name, other case", base(map[string]any{"name": "GOCHUJANG"}), 409, CodeNameTaken},
		{"missing category", base(map[string]any{"categoryId": nil}), 400, CodeInvalidCategory},
		{"category 0", base(map[string]any{"categoryId": 0}), 400, CodeInvalidCategory},
		{"unknown category", base(map[string]any{"categoryId": 9999}), 400, CodeInvalidCategory},
		{"huge category", base(map[string]any{"categoryId": 1 << 40}), 400, CodeInvalidCategory},
		{"missing location", base(map[string]any{"locationId": nil}), 400, CodeInvalidLocation},
		{"unknown location", base(map[string]any{"locationId": 9999}), 400, CodeInvalidLocation},
		{"shelf life 0", base(map[string]any{"shelfLifeDays": 0}), 400, CodeInvalidShelfLife},
		{"negative shelf life", base(map[string]any{"shelfLifeDays": -5}), 400, CodeInvalidShelfLife},
		{"shelf life 3651", base(map[string]any{"shelfLifeDays": 3651}), 400, CodeInvalidShelfLife},
		{"fractional shelf life", base(map[string]any{"shelfLifeDays": 1.5}), 400, CodeBadRequest},
		{"string shelf life", base(map[string]any{"shelfLifeDays": "7"}), 400, CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, b := f.postItem(t, f.sam, f.home, tt.body)
			wantError(t, resp, b, tt.status, tt.code)
		})
	}
	if n := f.count(t, `SELECT count(*) FROM catalog_items WHERE household_id = $1`, f.home); n != 1 {
		t.Errorf("home has %d custom items, want 1 (no failed create wrote a row)", n)
	}

	t.Run("not JSON", func(t *testing.T) {
		resp, b := f.req(t, http.MethodPost, itemsPath(f.home), f.sam, "{")
		wantError(t, resp, b, 400, CodeBadRequest)
	})
	t.Run("another household's custom name is allowed", func(t *testing.T) {
		f.createItem(t, f.sam, f.home, "away ONLY", nil)
	})
	t.Run("non-member gets 404, whatever the body", func(t *testing.T) {
		for _, body := range []map[string]any{base(nil), base(map[string]any{"name": ""}), base(map[string]any{"name": "Milk"})} {
			resp, b := f.postItem(t, f.bo, f.home, body)
			wantError(t, resp, b, 404, CodeNotFound)
		}
	})
	t.Run("missing household", func(t *testing.T) {
		resp, b := f.postItem(t, f.sam, newID(), base(nil))
		wantError(t, resp, b, 404, CodeNotFound)
	})
	t.Run("household ID not a UUID", func(t *testing.T) {
		resp, b := f.postItem(t, f.sam, "nope", base(nil))
		wantError(t, resp, b, 400, CodeBadRequest)
	})
}

// Two members creating the same name at the same moment: one 201, the rest
// 409 from the unique index, never a 500.
func TestCreateCatalogItemConcurrently(t *testing.T) {
	t.Parallel()
	f := newItemFixture(t)
	const rounds, perRound = 10, 4
	for r := 0; r < rounds; r++ {
		name := fmt.Sprintf("Race item %d", r)
		var wg sync.WaitGroup
		start := make(chan struct{})
		statuses := make([]int, perRound)
		bodies := make([]string, perRound)
		for i := 0; i < perRound; i++ {
			token := f.sam
			if i%2 == 1 {
				token = f.alex
			}
			wg.Add(1)
			go func(i int, token string) {
				defer wg.Done()
				<-start
				resp, b := f.postItem(t, token, f.home, map[string]any{
					"name": name, "categoryId": f.other, "locationId": f.pantry,
				})
				statuses[i], bodies[i] = resp.StatusCode, string(b)
			}(i, token)
		}
		close(start)
		wg.Wait()
		created := 0
		for i, s := range statuses {
			switch s {
			case http.StatusCreated:
				created++
			case http.StatusConflict:
				if !strings.Contains(bodies[i], CodeNameTaken) {
					t.Errorf("%s: 409 body %s, want code %s", name, bodies[i], CodeNameTaken)
				}
			default:
				t.Errorf("%s: status %d (body %s), want 201 or 409", name, s, bodies[i])
			}
		}
		if created != 1 {
			t.Errorf("%s: %d creates succeeded, want 1", name, created)
		}
	}
}

func TestGetCatalogItem(t *testing.T) {
	t.Parallel()
	f := newItemFixture(t)
	mine := f.createItem(t, f.sam, f.home, "Mine", map[string]any{"shelfLifeDays": 3})
	theirs := f.createItem(t, f.bo, f.away, "Theirs", nil)

	wantItem(t, f.getItem(t, f.alex, f.home, f.milk), itemResponse{
		ID: f.milk, Name: "Milk", CategoryID: f.dairy, LocationID: f.fridge, ShelfLifeDays: intp(7),
	})
	wantItem(t, f.getItem(t, f.alex, f.home, mine.ID), mine)
	// Bo sees the built-in in his own household.
	f.getItem(t, f.bo, f.away, f.milk)

	tests := []struct {
		name            string
		token, hh, item string
		status          int
		code            string
	}{
		{"another household's custom item", f.sam, f.home, theirs.ID, 404, CodeNotFound},
		{"missing item", f.sam, f.home, newID(), 404, CodeNotFound},
		{"non-member, built-in item", f.bo, f.home, f.milk, 404, CodeNotFound},
		{"non-member, the household's custom item", f.bo, f.home, mine.ID, 404, CodeNotFound},
		{"missing household", f.sam, newID(), f.milk, 404, CodeNotFound},
		{"item ID not a UUID", f.sam, f.home, "nope", 400, CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, b := f.req(t, http.MethodGet, itemPath(tt.hh, tt.item), tt.token, "")
			wantError(t, resp, b, tt.status, tt.code)
		})
	}
}

func TestUpdateCustomItem(t *testing.T) {
	t.Parallel()
	f := newItemFixture(t)
	it := f.createItem(t, f.sam, f.home, "Kimchi", map[string]any{"shelfLifeDays": 30})
	before := f.itemUpdatedAt(t, it.ID)

	t.Run("{} changes nothing", func(t *testing.T) {
		wantItem(t, f.updateItem(t, f.alex, f.home, it.ID, `{}`), it)
		if got := f.itemUpdatedAt(t, it.ID); !got.Equal(before) {
			t.Errorf("updated_at = %v, want %v unchanged", got, before)
		}
	})

	t.Run("category", func(t *testing.T) {
		it.CategoryID = f.produce
		wantItem(t, f.updateItem(t, f.alex, f.home, it.ID, jsonBody(t, map[string]any{"categoryId": f.produce})), it)
		if got := f.itemUpdatedAt(t, it.ID); !got.After(before) {
			t.Errorf("updated_at = %v, want after %v", got, before)
		}
	})
	t.Run("location and shelf life", func(t *testing.T) {
		it.LocationID, it.ShelfLifeDays = f.fridge, intp(14)
		wantItem(t, f.updateItem(t, f.alex, f.home, it.ID,
			jsonBody(t, map[string]any{"locationId": f.fridge, "shelfLifeDays": 14})), it)
	})
	t.Run("shelfLifeDays null clears it", func(t *testing.T) {
		it.ShelfLifeDays = nil
		wantItem(t, f.updateItem(t, f.alex, f.home, it.ID, `{"shelfLifeDays":null}`), it)
	})
	t.Run("all three", func(t *testing.T) {
		it.CategoryID, it.LocationID, it.ShelfLifeDays = f.other, f.freezer, intp(3650)
		wantItem(t, f.updateItem(t, f.sam, f.home, it.ID, jsonBody(t, map[string]any{
			"categoryId": f.other, "locationId": f.freezer, "shelfLifeDays": 3650,
		})), it)
	})
	// The row itself changed; nothing went to household_item_defaults.
	var cat, loc int
	var shelf *int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT default_category_id, default_location_id, shelf_life_days FROM catalog_items WHERE id = $1`, it.ID).
		Scan(&cat, &loc, &shelf); err != nil {
		t.Fatal(err)
	}
	if cat != f.other || loc != f.freezer || !eqIntp(shelf, intp(3650)) {
		t.Errorf("row = %d/%d/%s, want %d/%d/3650", cat, loc, fmtIntp(shelf), f.other, f.freezer)
	}
	if n := f.count(t, `SELECT count(*) FROM household_item_defaults`); n != 0 {
		t.Errorf("%d household_item_defaults rows, want 0", n)
	}

	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"null category", `{"categoryId":null}`, 400, CodeInvalidCategory},
		{"null location", `{"locationId":null}`, 400, CodeInvalidLocation},
		{"unknown category", `{"categoryId":9999}`, 400, CodeInvalidCategory},
		{"category 0", `{"categoryId":0}`, 400, CodeInvalidCategory},
		{"unknown location", `{"locationId":-1}`, 400, CodeInvalidLocation},
		{"shelf life 0", `{"shelfLifeDays":0}`, 400, CodeInvalidShelfLife},
		{"shelf life 3651", `{"shelfLifeDays":3651}`, 400, CodeInvalidShelfLife},
		{"fractional shelf life", `{"shelfLifeDays":2.5}`, 400, CodeBadRequest},
		{"valid field with a null category", fmt.Sprintf(`{"locationId":%d,"categoryId":null}`, f.pantry), 400, CodeInvalidCategory},
		{"not JSON", `{`, 400, CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, b := f.patchItem(t, f.sam, f.home, it.ID, tt.body)
			wantError(t, resp, b, tt.status, tt.code)
		})
	}
	// None of the failed requests changed anything.
	wantItem(t, f.getItem(t, f.sam, f.home, it.ID), it)

	t.Run("another household's custom item", func(t *testing.T) {
		resp, b := f.patchItem(t, f.bo, f.away, it.ID, `{"categoryId":1}`)
		wantError(t, resp, b, 404, CodeNotFound)
	})
	t.Run("non-member", func(t *testing.T) {
		resp, b := f.patchItem(t, f.bo, f.home, it.ID, `{"categoryId":1}`)
		wantError(t, resp, b, 404, CodeNotFound)
		resp, b = f.patchItem(t, f.bo, f.home, it.ID, `{"categoryId":null}`)
		wantError(t, resp, b, 404, CodeNotFound)
	})
	t.Run("missing item", func(t *testing.T) {
		resp, b := f.patchItem(t, f.sam, f.home, newID(), `{"categoryId":1}`)
		wantError(t, resp, b, 404, CodeNotFound)
	})
	wantItem(t, f.getItem(t, f.sam, f.home, it.ID), it)
}

func TestUpdateBuiltinItemDefaults(t *testing.T) {
	t.Parallel()
	f := newItemFixture(t)
	builtin := itemResponse{ID: f.milk, Name: "Milk", CategoryID: f.dairy, LocationID: f.fridge, ShelfLifeDays: intp(7)}

	t.Run("{} changes nothing and writes no row", func(t *testing.T) {
		wantItem(t, f.updateItem(t, f.sam, f.home, f.milk, `{}`), builtin)
		if n := f.overrideRows(t, f.home, f.milk); n != 0 {
			t.Errorf("%d override rows, want 0", n)
		}
	})

	t.Run("set category in home only", func(t *testing.T) {
		want := builtin
		want.CategoryID, want.CategoryOverridden = f.produce, true
		wantItem(t, f.updateItem(t, f.alex, f.home, f.milk, jsonBody(t, map[string]any{"categoryId": f.produce})), want)
		wantItem(t, f.getItem(t, f.sam, f.home, f.milk), want)

		var cat, loc *int
		var updatedBy string
		var updatedAt time.Time
		if err := f.pool.QueryRow(context.Background(),
			`SELECT category_id, location_id, updated_by::text, updated_at FROM household_item_defaults
			 WHERE household_id = $1 AND catalog_item_id = $2`, f.home, f.milk).
			Scan(&cat, &loc, &updatedBy, &updatedAt); err != nil {
			t.Fatal(err)
		}
		if !eqIntp(cat, &f.produce) || loc != nil || updatedBy != f.alexID || time.Since(updatedAt) > time.Minute {
			t.Errorf("override = %s/%s by %s at %v; want %d/null by %s now", fmtIntp(cat), fmtIntp(loc), updatedBy, updatedAt, f.produce, f.alexID)
		}

		// home's search sees the new category; away still sees the built-in.
		if got, _ := find(f.search(t, f.sam, f.home, "milk", 25), "Milk"); got.CategoryID != f.produce {
			t.Errorf("home's search: Milk category %d, want %d", got.CategoryID, f.produce)
		}
		if got, _ := find(f.search(t, f.bo, f.away, "milk", 25), "Milk"); got.CategoryID != f.dairy {
			t.Errorf("away's search: Milk category %d, want %d", got.CategoryID, f.dairy)
		}
		wantItem(t, f.getItem(t, f.bo, f.away, f.milk), builtin)
		if n := f.overrideRows(t, f.away, f.milk); n != 0 {
			t.Errorf("away has %d override rows, want 0", n)
		}
		// The catalog row itself is untouched.
		if n := f.count(t, `SELECT default_category_id FROM catalog_items WHERE id = $1`, f.milk); n != f.dairy {
			t.Errorf("built-in default category = %d, want %d", n, f.dairy)
		}
	})

	t.Run("set location keeps the category override", func(t *testing.T) {
		want := builtin
		want.CategoryID, want.CategoryOverridden = f.produce, true
		want.LocationID, want.LocationOverridden = f.freezer, true
		wantItem(t, f.updateItem(t, f.sam, f.home, f.milk, jsonBody(t, map[string]any{"locationId": f.freezer})), want)
	})

	t.Run("away sets its own override independently", func(t *testing.T) {
		want := builtin
		want.LocationID, want.LocationOverridden = f.pantry, true
		wantItem(t, f.updateItem(t, f.bo, f.away, f.milk, jsonBody(t, map[string]any{"locationId": f.pantry})), want)
		// home is unaffected.
		if got := f.getItem(t, f.sam, f.home, f.milk); got.LocationID != f.freezer || got.CategoryID != f.produce {
			t.Errorf("home's Milk = %+v after away's change, want produce/freezer", got)
		}
	})

	t.Run("null resets one field, the row stays for the other", func(t *testing.T) {
		want := builtin
		want.LocationID, want.LocationOverridden = f.freezer, true
		wantItem(t, f.updateItem(t, f.sam, f.home, f.milk, `{"categoryId":null}`), want)
		if n := f.overrideRows(t, f.home, f.milk); n != 1 {
			t.Errorf("%d override rows, want 1", n)
		}
	})

	t.Run("resetting the last field deletes the row", func(t *testing.T) {
		wantItem(t, f.updateItem(t, f.sam, f.home, f.milk, `{"locationId":null}`), builtin)
		if n := f.overrideRows(t, f.home, f.milk); n != 0 {
			t.Errorf("%d override rows, want 0", n)
		}
		// away keeps its override.
		if n := f.overrideRows(t, f.away, f.milk); n != 1 {
			t.Errorf("away has %d override rows, want 1", n)
		}
	})

	t.Run("the built-in value itself leaves no row", func(t *testing.T) {
		wantItem(t, f.updateItem(t, f.sam, f.home, f.milk,
			jsonBody(t, map[string]any{"categoryId": f.dairy, "locationId": f.fridge})), builtin)
		if n := f.overrideRows(t, f.home, f.milk); n != 0 {
			t.Errorf("%d override rows, want 0", n)
		}
		// Also when it replaces an existing override.
		f.updateItem(t, f.sam, f.home, f.milk, jsonBody(t, map[string]any{"categoryId": f.produce}))
		wantItem(t, f.updateItem(t, f.sam, f.home, f.milk, jsonBody(t, map[string]any{"categoryId": f.dairy})), builtin)
		if n := f.overrideRows(t, f.home, f.milk); n != 0 {
			t.Errorf("%d override rows, want 0", n)
		}
		// Built-in value for one field, a real override for the other: the
		// row keeps only the override.
		want := builtin
		want.LocationID, want.LocationOverridden = f.pantry, true
		wantItem(t, f.updateItem(t, f.sam, f.home, f.milk,
			jsonBody(t, map[string]any{"categoryId": f.dairy, "locationId": f.pantry})), want)
		if n := f.count(t, `SELECT count(*) FROM household_item_defaults
			WHERE household_id = $1 AND catalog_item_id = $2 AND category_id IS NULL AND location_id = $3`,
			f.home, f.milk, f.pantry); n != 1 {
			t.Errorf("want one row with category NULL and location %d", f.pantry)
		}
		f.updateItem(t, f.sam, f.home, f.milk, `{"locationId":null}`)
	})

	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"shelf life", `{"shelfLifeDays":10}`, 400, CodeNotOverridable},
		{"shelf life null", `{"shelfLifeDays":null}`, 400, CodeNotOverridable},
		{"shelf life with a category", fmt.Sprintf(`{"categoryId":%d,"shelfLifeDays":10}`, f.produce), 400, CodeNotOverridable},
		{"out-of-range shelf life", `{"shelfLifeDays":0}`, 400, CodeInvalidShelfLife},
		{"unknown category", `{"categoryId":9999}`, 400, CodeInvalidCategory},
		{"unknown location", `{"locationId":9999}`, 400, CodeInvalidLocation},
		{"not JSON", `[`, 400, CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, b := f.patchItem(t, f.sam, f.home, f.milk, tt.body)
			wantError(t, resp, b, tt.status, tt.code)
		})
	}
	t.Run("non-member", func(t *testing.T) {
		resp, b := f.patchItem(t, f.bo, f.home, f.milk, fmt.Sprintf(`{"categoryId":%d}`, f.produce))
		wantError(t, resp, b, 404, CodeNotFound)
		resp, b = f.patchItem(t, f.bo, f.home, f.milk, `{"shelfLifeDays":10}`)
		wantError(t, resp, b, 404, CodeNotFound)
	})
	// Nothing changed: no rows, and the built-in shelf life is as it was.
	wantItem(t, f.getItem(t, f.sam, f.home, f.milk), builtin)
	if n := f.overrideRows(t, f.home, f.milk); n != 0 {
		t.Errorf("%d override rows, want 0", n)
	}
	if n := f.count(t, `SELECT count(*) FROM item_events`); n != 0 {
		t.Errorf("%d item_events rows, want 0", n)
	}
}

// Changing an item's default category re-categorises the household's entries
// (entry category comes from the item, through effective_catalog_items);
// changing the default location only changes the suggestion for new entries.
func TestDefaultsChangeEffectsOnEntries(t *testing.T) {
	t.Parallel()
	f := newItemFixture(t)
	custom := f.createItem(t, f.sam, f.home, "Kombucha", map[string]any{"locationId": f.fridge})
	milkEntry, _ := f.createEntry(t, f.milk, nil)
	customEntry, _ := f.createEntry(t, custom.ID, nil)
	events := f.count(t, `SELECT count(*) FROM item_events`)

	// entryCategory is an entry's category as the view computes it.
	entryCategory := func(entry string) int {
		t.Helper()
		return f.count(t, `
			SELECT e.category_id FROM pantry_entries p
			JOIN effective_catalog_items e ON e.household_id = p.household_id AND e.catalog_item_id = p.catalog_item_id
			WHERE p.id = $1`, entry)
	}
	entryLocation := func(entry string) int {
		t.Helper()
		return f.count(t, `SELECT location_id FROM pantry_entries WHERE id = $1`, entry)
	}

	f.updateItem(t, f.sam, f.home, f.milk, jsonBody(t, map[string]any{"categoryId": f.produce, "locationId": f.freezer}))
	f.updateItem(t, f.sam, f.home, custom.ID, jsonBody(t, map[string]any{"categoryId": f.produce, "locationId": f.freezer}))

	for _, e := range []pantryEntry{milkEntry, customEntry} {
		if got := entryCategory(e.ID); got != f.produce {
			t.Errorf("entry %s category = %d, want %d", e.ID, got, f.produce)
		}
		if got := entryLocation(e.ID); got != f.fridge {
			t.Errorf("entry %s location = %d, want %d (unchanged)", e.ID, got, f.fridge)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM item_events`); n != events {
		t.Errorf("%d item_events rows after defaults changes, want %d", n, events)
	}
	// A new entry gets the new default location.
	for _, item := range []string{f.milk, custom.ID} {
		e, _ := f.createEntry(t, item, nil)
		if e.LocationID != f.freezer {
			t.Errorf("new entry of %s location = %d, want %d", item, e.LocationID, f.freezer)
		}
	}
	// Away's entries of Milk keep the built-in category.
	resp, b := f.post(t, f.bo, f.away, map[string]any{"id": newID(), "eventId": newID(), "catalogItemId": f.milk})
	var awayEntry pantryEntry
	wantStatus(t, resp, b, http.StatusCreated, &awayEntry)
	if got := entryCategory(awayEntry.ID); got != f.dairy {
		t.Errorf("away's Milk entry category = %d, want %d", got, f.dairy)
	}
	if awayEntry.LocationID != f.fridge {
		t.Errorf("away's new Milk entry location = %d, want %d", awayEntry.LocationID, f.fridge)
	}
}

func TestCatalogItemWritesRunAsAppRole(t *testing.T) {
	t.Parallel()
	f := newItemFixture(t)
	f.exec(t, `CREATE TABLE write_log (tbl text, role text, user_id text)`)
	f.exec(t, `GRANT INSERT ON write_log TO shelfkin_app`)
	f.exec(t, `CREATE FUNCTION log_write() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			INSERT INTO write_log VALUES (TG_TABLE_NAME, current_user, current_setting('app.user_id', true));
			RETURN NULL;
		END $$`)
	f.exec(t, `CREATE TRIGGER log_items AFTER INSERT OR UPDATE OR DELETE ON catalog_items FOR EACH ROW EXECUTE FUNCTION log_write()`)
	f.exec(t, `CREATE TRIGGER log_defaults AFTER INSERT OR UPDATE OR DELETE ON household_item_defaults FOR EACH ROW EXECUTE FUNCTION log_write()`)

	it := f.createItem(t, f.sam, f.home, "Logged", nil)                                 // 1 insert
	f.updateItem(t, f.sam, f.home, it.ID, `{"shelfLifeDays":5}`)                        // 1 update
	f.updateItem(t, f.sam, f.home, f.milk, fmt.Sprintf(`{"categoryId":%d}`, f.produce)) // 1 insert
	f.updateItem(t, f.sam, f.home, f.milk, `{"categoryId":null}`)                       // 1 update, 1 delete

	if n := f.count(t, `SELECT count(*) FROM write_log`); n != 5 {
		t.Errorf("%d logged writes, want 5", n)
	}
	if n := f.count(t, `SELECT count(*) FROM write_log WHERE role <> 'shelfkin_app' OR user_id IS DISTINCT FROM $1`, f.samID); n != 0 {
		t.Errorf("%d writes not as shelfkin_app with app.user_id = caller", n)
	}
}

// If row-level security ever rejects an override write that passed the API's
// item check, that is a bug in the check: 500, not 404.
func TestBuiltinOverrideRLSRejectionIs500(t *testing.T) {
	t.Parallel()
	f := newItemFixture(t)
	f.exec(t, `ALTER POLICY household_item_defaults_member ON household_item_defaults WITH CHECK (false)`)
	resp, b := f.patchItem(t, f.sam, f.home, f.milk, fmt.Sprintf(`{"categoryId":%d}`, f.produce))
	wantError(t, resp, b, http.StatusInternalServerError, CodeInternal)
}
