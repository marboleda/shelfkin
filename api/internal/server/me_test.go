package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/auth/authtest"
)

func TestValidateDisplayName(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Sam", "Sam", true},
		{"  Sam Lee  ", "Sam Lee", true},
		{"S", "S", true},
		{strings.Repeat("a", 50), strings.Repeat("a", 50), true},
		{" " + strings.Repeat("a", 50) + " ", strings.Repeat("a", 50), true},
		{strings.Repeat("é", 50), strings.Repeat("é", 50), true}, // characters, not bytes
		{strings.Repeat("a", 51), "", false},
		{"", "", false},
		{"   ", "", false},
		{"\t\n", "", false},
	}
	for _, tt := range tests {
		got, ok := ValidateDisplayName(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("ValidateDisplayName(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestValidTimezone(t *testing.T) {
	tests := []struct {
		tz string
		ok bool
	}{
		{"America/Toronto", true},
		{"UTC", true},
		{"Europe/London", true},
		{"Asia/Kolkata", true},
		{"Mars/Olympus", false},
		{"Local", false},
		{"", false},
		{"america/toronto", false},
		{" America/Toronto", false},
		{"../etc/passwd", false},
	}
	for _, tt := range tests {
		if got := ValidTimezone(tt.tz); got != tt.ok {
			t.Errorf("ValidTimezone(%q) = %v, want %v", tt.tz, got, tt.ok)
		}
	}
}

// meServer is the real API on a fresh database with authtest tokens.
type meServer struct {
	url      string
	pool     *pgxpool.Pool
	verifier *authtest.Verifier
}

func newMeServer(t *testing.T) *meServer {
	t.Helper()
	pool := dbtest.New(t)
	logger, _ := newLogger()
	v := authtest.NewVerifier()
	url := serve(t, NewHandler(NewAPI(pool, logger), auth.NewAuthenticator(v, pool), logger, nil))
	return &meServer{url: url, pool: pool, verifier: v}
}

// call sends method /me with token and body ("" for none) and returns the
// response and its body.
func (s *meServer) call(t *testing.T, method, token, body string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.url+"/me", r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", authtest.Bearer(token))
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

// user calls /me like call and decodes the 200 User response.
func (s *meServer) user(t *testing.T, method, token, body string) map[string]any {
	t.Helper()
	resp, b := s.call(t, method, token, body)
	return decodeUser(t, resp, b)
}

// decodeUser checks resp is a 200 User with exactly the five fields.
func decodeUser(t *testing.T, resp *http.Response, body []byte) map[string]any {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var u map[string]any
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&u); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	for _, k := range []string{"id", "displayName", "email", "timezone", "pushEnabled"} {
		if _, ok := u[k]; !ok {
			t.Errorf("body %s has no %q", body, k)
		}
	}
	if len(u) != 5 {
		t.Errorf("body %s has %d fields, want 5", body, len(u))
	}
	return u
}

func (s *meServer) updatedAt(t *testing.T, id string) time.Time {
	t.Helper()
	var at time.Time
	if err := s.pool.QueryRow(context.Background(), "SELECT updated_at FROM users WHERE id = $1", id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestGetMe(t *testing.T) {
	s := newMeServer(t)
	token := s.verifier.Issue(auth.Token{UID: "fb-sam", Email: "sam@example.com", Name: "Sam"})

	u := s.user(t, http.MethodGet, token, "")
	if u["displayName"] != "Sam" || u["email"] != "sam@example.com" || u["timezone"] != "UTC" || u["pushEnabled"] != false {
		t.Errorf("GET /me = %v", u)
	}
	var dbID string
	if err := s.pool.QueryRow(context.Background(), "SELECT id::text FROM users WHERE firebase_uid = 'fb-sam'").Scan(&dbID); err != nil {
		t.Fatal(err)
	}
	if u["id"] != dbID {
		t.Errorf("id = %v, want the users row id %s", u["id"], dbID)
	}

	// No email: null, not missing or "".
	anon := s.verifier.Issue(auth.Token{UID: "fb-anon"})
	u = s.user(t, http.MethodGet, anon, "")
	if u["email"] != nil || u["displayName"] != "Shelfkin user" {
		t.Errorf("GET /me for a user with no email = %v, want email null and the default name", u)
	}
}

// TestMeIsPerUser: each caller sees and changes only their own row.
func TestMeIsPerUser(t *testing.T) {
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	bob := s.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})

	s.user(t, http.MethodPatch, alice, `{"displayName":"Alicia"}`)
	if u := s.user(t, http.MethodGet, bob, ""); u["displayName"] != "Bob" {
		t.Errorf("Bob's profile = %v after Alice renamed herself", u)
	}
	if u := s.user(t, http.MethodGet, alice, ""); u["displayName"] != "Alicia" {
		t.Errorf("Alice's profile = %v, want Alicia", u)
	}
}

func TestPatchMe(t *testing.T) {
	s := newMeServer(t)
	token := s.verifier.Issue(auth.Token{UID: "fb-u", Email: "u@example.com", Name: "Original"})
	start := s.user(t, http.MethodGet, token, "")
	id := start["id"].(string)

	tests := []struct {
		name     string
		body     string
		wantName string
		wantTZ   string
	}{
		{"display name only", `{"displayName":"  New Name  "}`, "New Name", "UTC"},
		{"timezone only", `{"timezone":"America/Toronto"}`, "New Name", "America/Toronto"},
		{"both", `{"displayName":"Both","timezone":"Europe/Paris"}`, "Both", "Europe/Paris"},
		{"nulls are omitted fields", `{"displayName":null,"timezone":null}`, "Both", "Europe/Paris"},
		// pushEnabled is a real field since #21; email is read-only.
		{"unknown fields are ignored", `{"email":"other@example.com","favouriteColour":"blue"}`, "Both", "Europe/Paris"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := s.updatedAt(t, id)
			u := s.user(t, http.MethodPatch, token, tt.body)
			if u["displayName"] != tt.wantName || u["timezone"] != tt.wantTZ {
				t.Errorf("PATCH %s = %v, want name %q timezone %q", tt.body, u, tt.wantName, tt.wantTZ)
			}
			if u["id"] != id || u["email"] != "u@example.com" || u["pushEnabled"] != false {
				t.Errorf("PATCH changed other fields: %v", u)
			}
			// The response is what is stored.
			if got := s.user(t, http.MethodGet, token, ""); !equalMaps(got, u) {
				t.Errorf("GET after PATCH = %v, PATCH returned %v", got, u)
			}
			after := s.updatedAt(t, id)
			changed := !strings.Contains(tt.name, "omitted") && !strings.Contains(tt.name, "ignored")
			if changed && !after.After(before) {
				t.Errorf("updated_at %v not after %v", after, before)
			}
		})
	}
}

func equalMaps(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestPatchMeEmptyBody(t *testing.T) {
	s := newMeServer(t)
	token := s.verifier.Issue(auth.Token{UID: "fb-e", Name: "Emma"})
	start := s.user(t, http.MethodGet, token, "")
	before := s.updatedAt(t, start["id"].(string))

	for _, body := range []string{"", "{}"} {
		u := s.user(t, http.MethodPatch, token, body)
		if !equalMaps(u, start) {
			t.Errorf("PATCH with body %q = %v, want the unchanged %v", body, u, start)
		}
	}
	if after := s.updatedAt(t, start["id"].(string)); !after.Equal(before) {
		t.Errorf("a no-op PATCH changed updated_at from %v to %v", before, after)
	}
}

func TestPatchMeValidation(t *testing.T) {
	s := newMeServer(t)
	token := s.verifier.Issue(auth.Token{UID: "fb-v", Name: "Valid"})
	start := s.user(t, http.MethodGet, token, "")

	tests := []struct {
		name string
		body string
		code string
	}{
		{"empty name", `{"displayName":""}`, CodeInvalidDisplayName},
		{"blank name", `{"displayName":"    "}`, CodeInvalidDisplayName},
		{"51 characters", `{"displayName":"` + strings.Repeat("x", 51) + `"}`, CodeInvalidDisplayName},
		{"Mars/Olympus", `{"timezone":"Mars/Olympus"}`, CodeInvalidTimezone},
		{"Local", `{"timezone":"Local"}`, CodeInvalidTimezone},
		{"empty timezone", `{"timezone":""}`, CodeInvalidTimezone},
		{"valid name with bad timezone", `{"displayName":"Fine","timezone":"Nowhere/Land"}`, CodeInvalidTimezone},
		{"bad name with valid timezone", `{"displayName":"","timezone":"America/Toronto"}`, CodeInvalidDisplayName},
		{"not JSON", `{"displayName":`, CodeBadRequest},
		{"wrong type", `{"displayName":42}`, CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := s.call(t, http.MethodPatch, token, tt.body)
			assertError(t, resp, body, http.StatusBadRequest, tt.code)
		})
	}
	// Nothing was changed by the rejected requests, even partly.
	if u := s.user(t, http.MethodGet, token, ""); !equalMaps(u, start) {
		t.Errorf("after rejected PATCHes: %v, want unchanged %v", u, start)
	}
}

func TestPatchMeAccepted(t *testing.T) {
	s := newMeServer(t)
	token := s.verifier.Issue(auth.Token{UID: "fb-a"})
	for _, tz := range []string{"America/Toronto", "UTC", "Asia/Tokyo"} {
		u := s.user(t, http.MethodPatch, token, `{"timezone":"`+tz+`"}`)
		if u["timezone"] != tz {
			t.Errorf("timezone = %v, want %s", u["timezone"], tz)
		}
	}
	u := s.user(t, http.MethodPatch, token, `{"displayName":"`+strings.Repeat("é", 50)+`"}`)
	if u["displayName"] != strings.Repeat("é", 50) {
		t.Errorf("displayName = %v, want 50 é", u["displayName"])
	}
}
