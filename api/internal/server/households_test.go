package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// fakeAPI's households endpoints, so it keeps satisfying
// StrictServerInterface.

func (fakeAPI) ListHouseholds(context.Context, openapi.ListHouseholdsRequestObject) (openapi.ListHouseholdsResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) CreateHousehold(context.Context, openapi.CreateHouseholdRequestObject) (openapi.CreateHouseholdResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) GetHousehold(context.Context, openapi.GetHouseholdRequestObject) (openapi.GetHouseholdResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) UpdateHousehold(context.Context, openapi.UpdateHouseholdRequestObject) (openapi.UpdateHouseholdResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) DeleteHousehold(context.Context, openapi.DeleteHouseholdRequestObject) (openapi.DeleteHouseholdResponseObject, error) {
	return nil, errNotImplemented
}

func TestValidateHouseholdName(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Home", "Home", true},
		{"  Home  ", "Home", true},
		{"H", "H", true},
		{strings.Repeat("a", 50), strings.Repeat("a", 50), true},
		{strings.Repeat("é", 50), strings.Repeat("é", 50), true}, // characters, not bytes
		{strings.Repeat("a", 51), "", false},
		{"", "", false},
		{"   ", "", false},
		{"\t\n", "", false},
	}
	for _, tt := range tests {
		got, ok := ValidateHouseholdName(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("ValidateHouseholdName(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// household is a decoded Household response.
type household struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// req sends method path with token and body ("" for none) and returns the
// response and its body.
func (s *meServer) req(t *testing.T, method, path, token, body string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.url+path, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

// wantStatus fails the test unless resp has status want, and decodes a JSON
// body into v (if v is not nil).
func wantStatus(t *testing.T, resp *http.Response, body []byte, want int, v any) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status = %d, want %d (body %s)", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want, body)
	}
	if v != nil {
		if err := json.Unmarshal(body, v); err != nil {
			t.Fatalf("body %s: %v", body, err)
		}
	}
}

// wantError fails the test unless resp is an Error with status and code.
func wantError(t *testing.T, resp *http.Response, body []byte, status int, code string) {
	t.Helper()
	var e openapi.Error
	wantStatus(t, resp, body, status, &e)
	if e.Error.Code != code {
		t.Errorf("%s %s: code = %q, want %q (body %s)", resp.Request.Method, resp.Request.URL.Path, e.Error.Code, code, body)
	}
}

func (s *meServer) create(t *testing.T, token, name string) household {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name})
	resp, b := s.req(t, http.MethodPost, "/households", token, string(body))
	var h household
	wantStatus(t, resp, b, http.StatusCreated, &h)
	return h
}

func (s *meServer) list(t *testing.T, token string) []household {
	t.Helper()
	resp, b := s.req(t, http.MethodGet, "/households", token, "")
	var l struct {
		Households *[]household `json:"households"`
	}
	wantStatus(t, resp, b, http.StatusOK, &l)
	if l.Households == nil {
		t.Fatalf("GET /households = %s, want a households array", b)
	}
	return *l.Households
}

// userID returns the users.id of a token, creating the user if needed.
func (s *meServer) userID(t *testing.T, token string) string {
	t.Helper()
	return s.user(t, http.MethodGet, token, "")["id"].(string)
}

func (s *meServer) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (s *meServer) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func TestCreateListGetHousehold(t *testing.T) {
	s := newMeServer(t)
	sam := s.verifier.Issue(auth.Token{UID: "sam", Name: "Sam"})
	samID := s.userID(t, sam)

	if got := s.list(t, sam); len(got) != 0 {
		t.Fatalf("GET /households before creating any = %v, want []", got)
	}

	before := time.Now().Add(-time.Minute)
	home := s.create(t, sam, "Home")
	if home.Name != "Home" || home.Role != "owner" || home.ID == "" {
		t.Errorf("POST /households = %+v, want name Home, role owner", home)
	}
	if home.CreatedAt.Before(before) || home.CreatedAt.After(time.Now().Add(time.Minute)) {
		t.Errorf("createdAt = %v, want about now", home.CreatedAt)
	}

	// The response has exactly the four fields.
	resp, b := s.req(t, http.MethodGet, "/households/"+home.ID, sam, "")
	var raw map[string]any
	wantStatus(t, resp, b, http.StatusOK, &raw)
	if len(raw) != 4 {
		t.Errorf("GET /households/{id} = %s, want exactly id, name, role, createdAt", b)
	}
	var got household
	wantStatus(t, resp, b, http.StatusOK, &got)
	if got != home {
		t.Errorf("GET /households/{id} = %+v, POST returned %+v", got, home)
	}

	// One households row created by the caller and one owner row.
	if n := s.count(t, `SELECT count(*) FROM households WHERE id = $1 AND created_by = $2`, home.ID, samID); n != 1 {
		t.Errorf("%d households rows with created_by = caller, want 1", n)
	}
	if n := s.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1`, home.ID); n != 1 {
		t.Errorf("%d household_members rows, want 1", n)
	}
	if n := s.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1 AND user_id = $2 AND role = 'owner'`, home.ID, samID); n != 1 {
		t.Errorf("no owner row for the caller")
	}

	// Sorted by name, case-insensitively; duplicate names are allowed.
	s.create(t, sam, "cottage")
	s.create(t, sam, "Beach")
	s.create(t, sam, "home")
	var names []string
	for _, h := range s.list(t, sam) {
		names = append(names, strings.ToLower(h.Name))
		if h.Role != "owner" {
			t.Errorf("role in list = %q, want owner", h.Role)
		}
	}
	if want := "beach,cottage,home,home"; strings.Join(names, ",") != want {
		t.Errorf("GET /households names = %v, want %s", names, want)
	}
}

func TestHouseholdsHiddenFromNonMembers(t *testing.T) {
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	bob := s.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	home := s.create(t, alice, "Home")
	missing := "0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"

	if got := s.list(t, bob); len(got) != 0 {
		t.Errorf("Bob's households = %v, want []", got)
	}
	for _, id := range []string{home.ID, missing} {
		// Bob's view of Alice's household is the same as of one that never existed.
		resp, b := s.req(t, http.MethodGet, "/households/"+id, bob, "")
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
		resp, b = s.req(t, http.MethodPatch, "/households/"+id, bob, `{"name":"Mine"}`)
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
		resp, b = s.req(t, http.MethodDelete, "/households/"+id, bob, "")
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	}
	// The owner gets 404 for a household that never existed, too.
	resp, b := s.req(t, http.MethodDelete, "/households/"+missing, alice, "")
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)

	if got := s.list(t, alice); len(got) != 1 || got[0].Name != "Home" {
		t.Errorf("Alice's households after Bob's attempts = %+v, want Home unchanged", got)
	}
}

func TestHouseholdMemberCannotRenameOrDelete(t *testing.T) {
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	bob := s.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	home := s.create(t, alice, "Home")
	s.exec(t, `INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'member')`,
		home.ID, s.userID(t, bob))

	resp, b := s.req(t, http.MethodGet, "/households/"+home.ID, bob, "")
	var got household
	wantStatus(t, resp, b, http.StatusOK, &got)
	if got.ID != home.ID || got.Name != "Home" || got.Role != "member" || !got.CreatedAt.Equal(home.CreatedAt) {
		t.Errorf("member's GET = %+v, want Home with role member", got)
	}
	if l := s.list(t, bob); len(l) != 1 || l[0].ID != home.ID || l[0].Role != "member" {
		t.Errorf("member's list = %+v, want Home with role member", l)
	}

	resp, b = s.req(t, http.MethodPatch, "/households/"+home.ID, bob, `{"name":"Bob's"}`)
	wantError(t, resp, b, http.StatusForbidden, CodeOwnerOnly)
	resp, b = s.req(t, http.MethodDelete, "/households/"+home.ID, bob, "")
	wantError(t, resp, b, http.StatusForbidden, CodeOwnerOnly)

	if n := s.count(t, `SELECT count(*) FROM households WHERE id = $1 AND name = 'Home' AND updated_at = created_at`, home.ID); n != 1 {
		t.Error("the household changed after the member's rename and delete")
	}
	if n := s.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1`, home.ID); n != 2 {
		t.Errorf("%d members after the member's delete, want 2", n)
	}
}

func TestRenameHousehold(t *testing.T) {
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	home := s.create(t, alice, "Home")

	var before time.Time
	if err := s.pool.QueryRow(context.Background(), `SELECT updated_at FROM households WHERE id = $1`, home.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	resp, b := s.req(t, http.MethodPatch, "/households/"+home.ID, alice, `{"name":"  Cottage  "}`)
	var got household
	wantStatus(t, resp, b, http.StatusOK, &got)
	if got.ID != home.ID || got.Name != "Cottage" || got.Role != "owner" || !got.CreatedAt.Equal(home.CreatedAt) {
		t.Errorf("PATCH = %+v, want Cottage, owner, same id and createdAt as %+v", got, home)
	}
	var name string
	var after time.Time
	if err := s.pool.QueryRow(context.Background(), `SELECT name, updated_at FROM households WHERE id = $1`, home.ID).Scan(&name, &after); err != nil {
		t.Fatal(err)
	}
	if name != "Cottage" || !after.After(before) {
		t.Errorf("stored name %q, updated_at %v (before %v); want Cottage and a later updated_at", name, after, before)
	}
}

func TestHouseholdNameValidation(t *testing.T) {
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	home := s.create(t, alice, "Home")

	rejected := map[string]string{
		"empty":           `{"name":""}`,
		"whitespace only": `{"name":"   "}`,
		"51 characters":   `{"name":"` + strings.Repeat("a", 51) + `"}`,
		"missing":         `{}`,
	}
	for what, body := range rejected {
		t.Run(what, func(t *testing.T) {
			resp, b := s.req(t, http.MethodPost, "/households", alice, body)
			wantError(t, resp, b, http.StatusBadRequest, CodeInvalidName)
			resp, b = s.req(t, http.MethodPatch, "/households/"+home.ID, alice, body)
			wantError(t, resp, b, http.StatusBadRequest, CodeInvalidName)
		})
	}
	if n := s.count(t, `SELECT count(*) FROM households`); n != 1 {
		t.Errorf("%d households after rejected creates, want 1", n)
	}
	if n := s.count(t, `SELECT count(*) FROM households WHERE name = 'Home'`); n != 1 {
		t.Error("the household was renamed by a rejected PATCH")
	}

	// Not JSON: bad_request.
	resp, b := s.req(t, http.MethodPost, "/households", alice, `{"name":`)
	wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)

	fifty := strings.Repeat("a", 50)
	if h := s.create(t, alice, fifty); h.Name != fifty {
		t.Errorf("50-character name stored as %q", h.Name)
	}
	h := s.create(t, alice, "  Spaced Out  ")
	if h.Name != "Spaced Out" {
		t.Errorf("name with surrounding spaces returned as %q, want trimmed", h.Name)
	}
	if n := s.count(t, `SELECT count(*) FROM households WHERE id = $1 AND name = 'Spaced Out'`, h.ID); n != 1 {
		t.Error("name with surrounding spaces not stored trimmed")
	}
	resp, b = s.req(t, http.MethodPatch, "/households/"+home.ID, alice, `{"name":"`+fifty+`"}`)
	wantStatus(t, resp, b, http.StatusOK, nil)
}

func TestHouseholdMalformedID(t *testing.T) {
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		body := ""
		if method == http.MethodPatch {
			body = `{"name":"x"}`
		}
		resp, b := s.req(t, method, "/households/not-a-uuid", alice, body)
		wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)
	}
}

func TestDeleteHouseholdCascades(t *testing.T) {
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	bob := s.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	aliceID := s.userID(t, alice)
	home := s.create(t, alice, "Home")
	other := s.create(t, alice, "Other")

	// A member, an invite, a custom item, an item default, an entry and an
	// event in Home, and an entry in Other that must survive.
	s.exec(t, `INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'member')`, home.ID, s.userID(t, bob))
	s.exec(t, `INSERT INTO household_invites (household_id, code, created_by, expires_at) VALUES ($1, 'ABC123', $2, now() + interval '1 day')`, home.ID, aliceID)
	var item, builtIn, location string
	if err := s.pool.QueryRow(context.Background(), `
		INSERT INTO catalog_items (household_id, name, default_category_id, default_location_id)
		VALUES ($1, 'Oat milk', (SELECT min(id) FROM categories), (SELECT min(id) FROM locations))
		RETURNING id::text`, home.ID).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(context.Background(), `SELECT id::text FROM catalog_items WHERE household_id IS NULL LIMIT 1`).Scan(&builtIn); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(context.Background(), `SELECT min(id)::text FROM locations`).Scan(&location); err != nil {
		t.Fatal(err)
	}
	s.exec(t, `INSERT INTO household_item_defaults (household_id, catalog_item_id, location_id) VALUES ($1, $2, $3)`, home.ID, builtIn, location)
	for _, h := range []string{home.ID, other.ID} {
		s.exec(t, `
			WITH e AS (
			    INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level)
			    VALUES (uuidv7(), $1, $2, $3, 'full') RETURNING id
			)
			INSERT INTO item_events (id, household_id, entry_id, catalog_item_id, type, snapshot)
			SELECT uuidv7(), $1, e.id, $2, 'entry_created', '{}' FROM e`, h, builtIn, location)
	}
	s.exec(t, `INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level) VALUES (uuidv7(), $1, $2, $3, 'low')`, home.ID, item, location)

	resp, b := s.req(t, http.MethodDelete, "/households/"+home.ID, alice, "")
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	if len(b) != 0 {
		t.Errorf("DELETE body = %q, want empty", b)
	}

	for _, table := range []string{"households WHERE id", "household_members WHERE household_id",
		"household_invites WHERE household_id", "catalog_items WHERE household_id",
		"household_item_defaults WHERE household_id", "pantry_entries WHERE household_id",
		"item_events WHERE household_id"} {
		if n := s.count(t, `SELECT count(*) FROM `+table+` = $1`, home.ID); n != 0 {
			t.Errorf("%d rows left in %s = Home, want 0", n, table)
		}
	}
	for _, table := range []string{"pantry_entries", "item_events"} {
		if n := s.count(t, `SELECT count(*) FROM `+table+` WHERE household_id = $1`, other.ID); n != 1 {
			t.Errorf("%d %s rows in Other, want 1", n, table)
		}
	}
	resp, b = s.req(t, http.MethodGet, "/households/"+home.ID, alice, "")
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
}

func TestDeleteOnlyHouseholdLeavesEmptyList(t *testing.T) {
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	home := s.create(t, alice, "Home")

	resp, b := s.req(t, http.MethodDelete, "/households/"+home.ID, alice, "")
	wantStatus(t, resp, b, http.StatusNoContent, nil)

	resp, b = s.req(t, http.MethodGet, "/households", alice, "")
	wantStatus(t, resp, b, http.StatusOK, nil)
	if strings.TrimSpace(string(b)) != `{"households":[]}` {
		t.Errorf("GET /households = %s, want {\"households\":[]}", b)
	}
}

func TestHouseholdsRequireAuth(t *testing.T) {
	s := newMeServer(t)
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/households"},
		{http.MethodPost, "/households"},
		{http.MethodGet, "/households/0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"},
		{http.MethodPatch, "/households/0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"},
		{http.MethodDelete, "/households/0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"},
	} {
		resp, b := s.req(t, r.method, r.path, "not-issued", "")
		wantError(t, resp, b, http.StatusUnauthorized, CodeUnauthorized)
	}
}
