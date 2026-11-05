package server

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/auth/authtest"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// fakeAPI's catalog endpoints, so it keeps satisfying StrictServerInterface.

func (fakeAPI) SearchCatalog(context.Context, openapi.SearchCatalogRequestObject) (openapi.SearchCatalogResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) ListCategories(context.Context, openapi.ListCategoriesRequestObject) (openapi.ListCategoriesResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) ListLocations(context.Context, openapi.ListLocationsRequestObject) (openapi.ListLocationsResponseObject, error) {
	return nil, errNotImplemented
}

func TestValidateSearchQuery(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", true},
		{"   ", "", true},
		{" milk ", "milk", true},
		{strings.Repeat("a", 100), strings.Repeat("a", 100), true},
		{" " + strings.Repeat("a", 100) + " ", strings.Repeat("a", 100), true},
		{strings.Repeat("é", 100), strings.Repeat("é", 100), true}, // characters, not bytes
		{strings.Repeat("a", 101), "", false},
	}
	for _, tt := range tests {
		got, ok := ValidateSearchQuery(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("ValidateSearchQuery(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// catalogItem is a decoded CatalogItem.
type catalogItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	IsCustom      bool   `json:"isCustom"`
	CategoryID    int    `json:"categoryId"`
	LocationID    int    `json:"locationId"`
	ShelfLifeDays *int   `json:"shelfLifeDays"`
}

// searchPath returns the search URL path for household with q and, if not
// empty, limit.
func searchPath(household, q, limit string) string {
	v := url.Values{"q": {q}}
	if limit != "" {
		v.Set("limit", limit)
	}
	return "/households/" + household + "/catalog/search?" + v.Encode()
}

// search runs a search that must succeed and returns the items.
func (s *meServer) search(t *testing.T, token, household, q string, limit int) []catalogItem {
	t.Helper()
	resp, b := s.req(t, http.MethodGet, searchPath(household, q, strconv.Itoa(limit)), token, "")
	var out struct {
		Items *[]catalogItem `json:"items"`
	}
	wantStatus(t, resp, b, http.StatusOK, &out)
	if out.Items == nil {
		t.Fatalf("search %q = %s, want an items array", q, b)
	}
	return *out.Items
}

func names(items []catalogItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

func find(items []catalogItem, name string) (catalogItem, bool) {
	for _, it := range items {
		if it.Name == name {
			return it, true
		}
	}
	return catalogItem{}, false
}

// lookupID returns the id of key in table (categories or locations).
func (s *meServer) lookupID(t *testing.T, table, key string) int {
	t.Helper()
	return s.count(t, `SELECT id FROM `+table+` WHERE key = $1`, key)
}

// addCustomItem adds a custom item to household and returns its ID.
func (s *meServer) addCustomItem(t *testing.T, household, name string, shelfLife any) string {
	t.Helper()
	var id string
	err := s.pool.QueryRow(context.Background(),
		`INSERT INTO catalog_items (household_id, name, default_category_id, default_location_id, shelf_life_days)
		 VALUES ($1, $2, (SELECT id FROM categories WHERE key = 'other'), (SELECT id FROM locations WHERE key = 'pantry'), $3)
		 RETURNING id`, household, name, shelfLife).Scan(&id)
	if err != nil {
		t.Fatalf("adding custom item %q: %v", name, err)
	}
	return id
}

// searchFixture is a server with one household owned by sam.
type searchFixture struct {
	*meServer
	sam  string
	home string
}

func newSearchFixture(t *testing.T) searchFixture {
	t.Helper()
	s := newMeServer(t)
	sam := s.verifier.Issue(auth.Token{UID: "sam", Name: "Sam"})
	return searchFixture{meServer: s, sam: sam, home: s.create(t, sam, "Home").ID}
}

func TestSearchCatalogRanking(t *testing.T) {
	t.Parallel()
	f := newSearchFixture(t)

	t.Run("exact match first", func(t *testing.T) {
		got := f.search(t, f.sam, f.home, "milk", 25)
		if len(got) == 0 || got[0].Name != "Milk" {
			t.Fatalf("milk = %q, want Milk first", names(got))
		}
	})
	t.Run("case-insensitive", func(t *testing.T) {
		got := f.search(t, f.sam, f.home, "MiLK", 25)
		if len(got) == 0 || got[0].Name != "Milk" {
			t.Fatalf("MiLK = %q, want Milk first", names(got))
		}
	})
	t.Run("prefix before word prefix", func(t *testing.T) {
		got := names(f.search(t, f.sam, f.home, "mil", 25))
		milk, oat := slices.Index(got, "Milk"), slices.Index(got, "Oat milk")
		if milk < 0 || oat < 0 || milk > oat {
			t.Fatalf("mil = %q, want Milk before Oat milk", got)
		}
	})
	t.Run("word prefix ties by shorter name then alphabetically", func(t *testing.T) {
		// "mil": Milk starts with it; then the names with a word "milk...",
		// shortest first.
		got := names(f.search(t, f.sam, f.home, "mil", 25))
		want := []string{"Milk", "Oat milk", "Almond milk", "Coconut milk", "Evaporated milk", "Sweetened condensed milk"}
		if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
			t.Fatalf("mil = %q, want it to start with %q", got, want)
		}
	})
	t.Run("word prefix after a hyphen or space", func(t *testing.T) {
		// Flour tortillas starts with "flour"; the rest have a word that does,
		// shortest first and then alphabetically (All-purpose flour and Whole
		// wheat flour are both 17 characters).
		got := names(f.search(t, f.sam, f.home, "flour", 25))
		want := []string{"Flour tortillas", "Cake flour", "Bread flour", "Almond flour", "All-purpose flour", "Whole wheat flour"}
		if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
			t.Fatalf("flour = %q, want it to start with %q", got, want)
		}
		if !slices.Contains(got, "All-purpose flour") {
			t.Errorf("flour = %q, want All-purpose flour", got)
		}
	})
	t.Run("fuzzy match for a misspelling", func(t *testing.T) {
		got := f.search(t, f.sam, f.home, "tomatos", 25)
		if len(got) == 0 || got[0].Name != "Tomatoes" {
			t.Fatalf("tomatos = %q, want Tomatoes first", names(got))
		}
	})
	t.Run("fuzzy matches after word prefix matches", func(t *testing.T) {
		// "tomato" is a prefix of Tomato paste, Tomato sauce and Tomatoes and
		// a word prefix of Crushed tomatoes; nothing fuzzy comes before them.
		got := names(f.search(t, f.sam, f.home, "tomato", 25))
		want := []string{"Tomatoes", "Tomato paste", "Tomato sauce", "Crushed tomatoes", "Canned diced tomatoes"}
		if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
			t.Fatalf("tomato = %q, want it to start with %q", got, want)
		}
	})
	t.Run("short queries match prefixes and word prefixes only", func(t *testing.T) {
		for _, q := range []string{"to", "t"} {
			got := names(f.search(t, f.sam, f.home, q, 25))
			if len(got) == 0 {
				t.Fatalf("%s = [], want matches", q)
			}
			for _, n := range got {
				ln := strings.ToLower(n)
				wordPrefix := strings.HasPrefix(ln, q) || strings.Contains(ln, " "+q) || strings.Contains(ln, "-"+q)
				if !wordPrefix {
					t.Errorf("%s matched %q, which has no word starting with %q", q, n, q)
				}
			}
			if slices.Contains(got, "Potatoes") {
				t.Errorf("%s matched Potatoes (an infix match)", q)
			}
		}
	})
	t.Run("no fuzzy matches for 2 characters", func(t *testing.T) {
		// "mk" is not a prefix of anything, so only a fuzzy match could
		// return a row.
		if got := f.search(t, f.sam, f.home, "mk", 25); len(got) != 0 {
			t.Errorf("mk = %q, want []", names(got))
		}
	})
}

func TestSearchCatalogLiteralWildcards(t *testing.T) {
	t.Parallel()
	f := newSearchFixture(t)
	for _, n := range []string{"5% vinegar", "50 grams rice", "a_b sauce", "abc sauce", `a\b sauce`} {
		f.addCustomItem(t, f.home, n, nil)
	}
	// Two-character queries have no fuzzy matches, so these show exactly what
	// the prefix matching does. Unescaped, "5%" would match "50 grams rice",
	// "a_" every name starting with "a", and "%" or "_" every name.
	tests := []struct {
		q    string
		want []string
	}{
		{"5%", []string{"5% vinegar"}},
		{"a_", []string{"a_b sauce"}},
		{`a\`, []string{`a\b sauce`}},
		{"%", nil},
		{"_", nil},
		{"%%", nil},
		{"__", nil},
	}
	for _, tt := range tests {
		got := names(f.search(t, f.sam, f.home, tt.q, 25))
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("search %q = %q, want %q", tt.q, got, tt.want)
		}
	}

	// A longer query with % ranks the literal match first and does not
	// match everything. (Fuzzy matching ignores punctuation, so "50 grams
	// rice" may follow as a fuzzy match.)
	f.addCustomItem(t, f.home, "50% cream", nil)
	got := names(f.search(t, f.sam, f.home, "50%", 25))
	if len(got) == 0 || got[0] != "50% cream" || len(got) > 3 || slices.Contains(got, "Milk") {
		t.Errorf("search 50%% = %q, want 50%% cream first and only a few items", got)
	}
}

func TestSearchCatalogHouseholdIsolation(t *testing.T) {
	t.Parallel()
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	bob := s.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	a := s.create(t, alice, "A").ID
	b := s.create(t, bob, "B").ID

	// A's custom item: found by A, never by B, even on an exact match.
	itemID := s.addCustomItem(t, a, "Zebra cake", 3)
	got := s.search(t, alice, a, "zebra cake", 25)
	if len(got) == 0 || got[0].Name != "Zebra cake" || got[0].ID != itemID || !got[0].IsCustom {
		t.Errorf("A searching zebra cake = %+v, want A's custom item first", got)
	} else if got[0].ShelfLifeDays == nil || *got[0].ShelfLifeDays != 3 {
		t.Errorf("Zebra cake shelfLifeDays = %v, want 3", got[0].ShelfLifeDays)
	}
	if got := s.search(t, bob, b, "zebra cake", 25); len(got) != 0 {
		t.Errorf("B searching zebra cake = %q, want []", names(got))
	}

	// B overrides the location of the built-in Milk: B sees it, A does not.
	fridge, freezer := s.lookupID(t, "locations", "fridge"), s.lookupID(t, "locations", "freezer")
	s.exec(t, `INSERT INTO household_item_defaults (household_id, catalog_item_id, location_id)
	           SELECT $1, id, $2 FROM catalog_items WHERE household_id IS NULL AND name = 'Milk'`, b, freezer)
	milkA, okA := find(s.search(t, alice, a, "milk", 25), "Milk")
	milkB, okB := find(s.search(t, bob, b, "milk", 25), "Milk")
	if !okA || !okB {
		t.Fatalf("Milk missing from a search (A %v, B %v)", okA, okB)
	}
	if milkB.LocationID != freezer {
		t.Errorf("B's Milk locationId = %d, want freezer (%d)", milkB.LocationID, freezer)
	}
	if milkA.LocationID != fridge {
		t.Errorf("A's Milk locationId = %d, want fridge (%d)", milkA.LocationID, fridge)
	}
	if milkA.CategoryID != milkB.CategoryID || milkA.ID != milkB.ID || milkA.IsCustom || milkB.IsCustom {
		t.Errorf("Milk differs beyond the location: A %+v, B %+v", milkA, milkB)
	}
}

func TestSearchCatalogFields(t *testing.T) {
	t.Parallel()
	f := newSearchFixture(t)
	f.addCustomItem(t, f.home, "Mystery jar", nil)

	resp, b := f.req(t, http.MethodGet, searchPath(f.home, "mystery jar", ""), f.sam, "")
	var out struct {
		Items []map[string]any `json:"items"`
	}
	wantStatus(t, resp, b, http.StatusOK, &out)
	if len(out.Items) != 1 {
		t.Fatalf("search = %s, want one item", b)
	}
	keys := make([]string, 0, 6)
	for k := range out.Items[0] {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"categoryId", "id", "isCustom", "locationId", "name", "shelfLifeDays"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("item keys = %q, want %q", keys, want)
	}
	if v, ok := out.Items[0]["shelfLifeDays"]; !ok || v != nil {
		t.Errorf("shelfLifeDays = %v, want null", v)
	}

	// A built-in item carries its seeded values.
	milk, ok := find(f.search(t, f.sam, f.home, "milk", 1), "Milk")
	if !ok {
		t.Fatal("Milk not found")
	}
	var cat, loc, days int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT default_category_id, default_location_id, shelf_life_days FROM catalog_items WHERE household_id IS NULL AND name = 'Milk'`).
		Scan(&cat, &loc, &days); err != nil {
		t.Fatal(err)
	}
	if milk.CategoryID != cat || milk.LocationID != loc || milk.ShelfLifeDays == nil || *milk.ShelfLifeDays != days || milk.IsCustom {
		t.Errorf("Milk = %+v, want category %d, location %d, shelf life %d, built-in", milk, cat, loc, days)
	}
}

func TestSearchCatalogValidation(t *testing.T) {
	t.Parallel()
	f := newSearchFixture(t)

	for _, q := range []string{"", "   ", "\t"} {
		if got := f.search(t, f.sam, f.home, q, 10); len(got) != 0 {
			t.Errorf("q=%q = %q, want []", q, names(got))
		}
	}
	// No q at all.
	resp, b := f.req(t, http.MethodGet, "/households/"+f.home+"/catalog/search", f.sam, "")
	wantStatus(t, resp, b, http.StatusOK, nil)
	if strings.TrimSpace(string(b)) != `{"items":[]}` {
		t.Errorf("no q = %s, want {\"items\":[]}", b)
	}

	// 100 characters (after trimming) is fine; 101 is not.
	f.search(t, f.sam, f.home, "  "+strings.Repeat("a", 100)+"  ", 10)
	resp, b = f.req(t, http.MethodGet, searchPath(f.home, strings.Repeat("a", 101), ""), f.sam, "")
	wantError(t, resp, b, http.StatusBadRequest, CodeInvalidQuery)

	for _, limit := range []string{"0", "26", "-1"} {
		resp, b := f.req(t, http.MethodGet, searchPath(f.home, "milk", limit), f.sam, "")
		wantError(t, resp, b, http.StatusBadRequest, CodeInvalidLimit)
	}
	resp, b = f.req(t, http.MethodGet, searchPath(f.home, "milk", "ten"), f.sam, "")
	wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)
	resp, b = f.req(t, http.MethodGet, searchPath("not-a-uuid", "milk", ""), f.sam, "")
	wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)

	// The limit defaults to 10 and caps the results; 1 and 25 are allowed.
	resp, b = f.req(t, http.MethodGet, searchPath(f.home, "s", ""), f.sam, "")
	var out struct {
		Items []catalogItem `json:"items"`
	}
	wantStatus(t, resp, b, http.StatusOK, &out)
	if len(out.Items) != 10 {
		t.Errorf("q=s with no limit returned %d items, want 10", len(out.Items))
	}
	if got := f.search(t, f.sam, f.home, "s", 1); len(got) != 1 {
		t.Errorf("limit=1 returned %d items", len(got))
	}
	if got := f.search(t, f.sam, f.home, "s", 25); len(got) != 25 {
		t.Errorf("limit=25 returned %d items, want 25", len(got))
	}
}

func TestSearchCatalogNotMember(t *testing.T) {
	t.Parallel()
	f := newSearchFixture(t)
	eve := f.verifier.Issue(auth.Token{UID: "eve", Name: "Eve"})

	for _, q := range []string{"milk", ""} {
		resp, b := f.req(t, http.MethodGet, searchPath(f.home, q, ""), eve, "")
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	}
	resp, b := f.req(t, http.MethodGet, searchPath("00000000-0000-7000-8000-000000000000", "milk", ""), f.sam, "")
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)

	resp, b = f.req(t, http.MethodGet, searchPath(f.home, "milk", ""), "not-a-token", "")
	wantError(t, resp, b, http.StatusUnauthorized, CodeUnauthorized)
}

// queryCounter is a pgx tracer that records every statement.
type queryCounter struct {
	mu  sync.Mutex
	sql []string
}

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sql = append(c.sql, d.SQL)
	return ctx
}

func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *queryCounter) reset() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.sql
	c.sql = nil
	return out
}

// TestSearchCatalogOneQuery checks a search runs one SQL query besides the
// transaction itself and the setup of dbtx (SET LOCAL ROLE and set_config), whatever the
// number of results, including for a non-member.
func TestSearchCatalogOneQuery(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	cfg := pool.Config()
	counter := &queryCounter{}
	cfg.ConnConfig.Tracer = counter
	traced, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)

	logger, _ := newLogger()
	v := authtest.NewVerifier()
	// The authenticator (user lookup) uses the untraced pool.
	s := &meServer{
		url:      serve(t, NewHandler(NewAPI(traced, logger), auth.NewAuthenticator(v, pool), logger, nil)),
		pool:     pool,
		verifier: v,
	}
	sam := v.Issue(auth.Token{UID: "sam", Name: "Sam"})
	eve := v.Issue(auth.Token{UID: "eve", Name: "Eve"})
	home := s.create(t, sam, "Home").ID
	s.userID(t, eve)

	for _, c := range []struct {
		token, q string
		status   int
	}{
		{sam, "s", http.StatusOK},
		{sam, "tomatos", http.StatusOK},
		{sam, "", http.StatusOK},
		{eve, "milk", http.StatusNotFound},
	} {
		counter.reset()
		resp, b := s.req(t, http.MethodGet, searchPath(home, c.q, "25"), c.token, "")
		wantStatus(t, resp, b, c.status, nil)
		var queries []string
		for _, sql := range counter.reset() {
			switch {
			case sql == "begin", sql == "commit", sql == "rollback":
				continue
			case strings.HasPrefix(sql, "SET LOCAL ROLE"), strings.Contains(sql, "set_config('app.user_id'"):
				continue
			}
			queries = append(queries, sql)
		}
		if len(queries) != 1 || !strings.Contains(queries[0], "search_catalog") {
			t.Errorf("q=%q ran %d queries, want only the search_catalog query: %q", c.q, len(queries), queries)
		}
	}
}

func TestListCategoriesAndLocations(t *testing.T) {
	t.Parallel()
	s := newMeServer(t)
	// A signed-in user with no household.
	nora := s.verifier.Issue(auth.Token{UID: "nora", Name: "Nora"})

	// The keys and names from #6, in sort order.
	tests := []struct {
		path string
		want [][2]string
	}{
		{"/categories", [][2]string{
			{"produce", "Produce"}, {"dairy", "Dairy"}, {"meat_seafood", "Meat & seafood"},
			{"grains", "Grains"}, {"baking", "Baking"}, {"spices", "Spices"},
			{"canned", "Canned goods"}, {"condiments", "Condiments & sauces"},
			{"oils", "Oils & vinegars"}, {"snacks", "Snacks"}, {"beverages", "Beverages"},
			{"other", "Other"},
		}},
		{"/locations", [][2]string{
			{"pantry", "Pantry"}, {"fridge", "Fridge"}, {"freezer", "Freezer"},
			{"spice_rack", "Spice rack"}, {"counter", "Counter"},
		}},
	}
	for _, tt := range tests {
		resp, b := s.req(t, http.MethodGet, tt.path, nora, "")
		var out struct {
			Items []map[string]any `json:"items"`
		}
		wantStatus(t, resp, b, http.StatusOK, &out)
		if len(out.Items) != len(tt.want) {
			t.Fatalf("GET %s = %s, want %d items", tt.path, b, len(tt.want))
		}
		table := strings.TrimPrefix(tt.path, "/")
		for i, it := range out.Items {
			if len(it) != 3 {
				t.Errorf("GET %s item %d = %v, want exactly id, key, name", tt.path, i, it)
			}
			key, name := it["key"], it["name"]
			if key != tt.want[i][0] || name != tt.want[i][1] {
				t.Errorf("GET %s item %d = %v %v, want %s %s", tt.path, i, key, name, tt.want[i][0], tt.want[i][1])
			}
			if id, ok := it["id"].(float64); !ok || int(id) != s.lookupID(t, table, tt.want[i][0]) {
				t.Errorf("GET %s item %d id = %v, want the %s row's id", tt.path, i, it["id"], tt.want[i][0])
			}
		}

		resp, b = s.req(t, http.MethodGet, tt.path, "not-a-token", "")
		wantError(t, resp, b, http.StatusUnauthorized, CodeUnauthorized)
	}
}
