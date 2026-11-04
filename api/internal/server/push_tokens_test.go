package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

func TestValidPushToken(t *testing.T) {
	tests := []struct {
		token string
		ok    bool
	}{
		{"ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]", true},
		{"ExpoPushToken[xxxxxxxxxxxxxxxxxxxxxx]", true},
		{"ExponentPushToken[AbC-123_x.y:z]", true},
		{"ExponentPushToken[" + strings.Repeat("a", MaxPushTokenLength-len("ExponentPushToken[]")) + "]", true},
		{"ExponentPushToken[" + strings.Repeat("a", MaxPushTokenLength-len("ExponentPushToken[]")+1) + "]", false},
		{"", false},
		{"ExponentPushToken[]", false},
		{"ExponentPushToken[abc", false},
		{"ExponentPushTokenabc]", false},
		{"exponentpushtoken[abc]", false},
		{"FooPushToken[abc]", false},
		{"abc", false},
		{" ExponentPushToken[abc]", false},
		{"ExponentPushToken[abc] ", false},
		{"ExponentPushToken[a b]", false},
		{"ExponentPushToken[a]b]", false},
		{"ExponentPushToken[a[b]", false},
		{"ExponentPushToken[a/b]", false},
		{"ExponentPushToken[é]", false},
		{"0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80", false},
	}
	for _, tt := range tests {
		if got := ValidPushToken(tt.token); got != tt.ok {
			t.Errorf("ValidPushToken(%q) = %v, want %v", tt.token, got, tt.ok)
		}
	}
}

func TestValidPushPlatform(t *testing.T) {
	for _, p := range []string{"ios", "android", "web"} {
		if !ValidPushPlatform(openapi.PushPlatform(p)) {
			t.Errorf("ValidPushPlatform(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"", "IOS", "windows", "expo", " ios"} {
		if ValidPushPlatform(openapi.PushPlatform(p)) {
			t.Errorf("ValidPushPlatform(%q) = true, want false", p)
		}
	}
}

// do sends method path with token and body ("" for none) and returns the
// response and its body.
func (s *meServer) do(t *testing.T, method, path, token, body string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.url+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
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

type pushTokenBody struct {
	Token      string    `json:"token"`
	Platform   string    `json:"platform"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// register POSTs pushToken for the caller and decodes the 200 response.
func (s *meServer) register(t *testing.T, caller, pushToken, platform string) pushTokenBody {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": pushToken, "platform": platform})
	if err != nil {
		t.Fatal(err)
	}
	resp, b := s.do(t, http.MethodPost, "/me/push-tokens", caller, string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /me/push-tokens %s: status = %d, want 200 (body %s)", pushToken, resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("body %s: %v", b, err)
	}
	if len(raw) != 3 {
		t.Errorf("body %s has %d fields, want token, platform and lastSeenAt", b, len(raw))
	}
	var got pushTokenBody
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("body %s: %v", b, err)
	}
	if got.Token != pushToken || got.Platform != platform || got.LastSeenAt.IsZero() {
		t.Errorf("POST /me/push-tokens = %+v, want token %s, platform %s and a lastSeenAt", got, pushToken, platform)
	}
	return got
}

// deleteToken DELETEs pushToken (URL-encoded) for the caller and checks for 204.
func (s *meServer) deleteToken(t *testing.T, caller, pushToken string) {
	t.Helper()
	resp, b := s.do(t, http.MethodDelete, "/me/push-tokens/"+url.PathEscape(pushToken), caller, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE %s: status = %d, want 204 (body %s)", pushToken, resp.StatusCode, b)
	}
	if len(b) != 0 {
		t.Errorf("DELETE %s: body %q, want none", pushToken, b)
	}
}

type tokenRow struct {
	UserID     string
	Platform   string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// tokenRows returns every push_tokens row by token, straight from the
// database.
func (s *meServer) tokenRows(t *testing.T) map[string]tokenRow {
	t.Helper()
	rows, err := s.pool.Query(context.Background(),
		"SELECT token, user_id::text, coalesce(platform, ''), created_at, last_seen_at FROM push_tokens")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	m := map[string]tokenRow{}
	for rows.Next() {
		var token string
		var r tokenRow
		if err := rows.Scan(&token, &r.UserID, &r.Platform, &r.CreatedAt, &r.LastSeenAt); err != nil {
			t.Fatal(err)
		}
		m[token] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return m
}

// userID returns the caller's users.id through GET /me.
func (s *meServer) userID(t *testing.T, caller string) string {
	t.Helper()
	return s.user(t, http.MethodGet, caller, "")["id"].(string)
}

const (
	phoneToken  = "ExponentPushToken[phone-aaaaaaaaaaaaaaaa]"
	tabletToken = "ExpoPushToken[tablet-bbbbbbbbbbbbbbbb]"
	laptopToken = "ExponentPushToken[laptop-cccccccccccccccc]"
)

// TestRegisterPushToken: register, refresh (same row, newer last_seen_at),
// register on a second device.
func TestRegisterPushToken(t *testing.T) {
	s := newMeServer(t)
	sam := s.verifier.Issue(auth.Token{UID: "sam", Name: "Sam"})
	samID := s.userID(t, sam)

	first := s.register(t, sam, phoneToken, "ios")
	rows := s.tokenRows(t)
	if len(rows) != 1 || rows[phoneToken].UserID != samID || rows[phoneToken].Platform != "ios" {
		t.Fatalf("after register: rows = %+v, want one ios row for Sam", rows)
	}
	if !rows[phoneToken].LastSeenAt.Equal(first.LastSeenAt) {
		t.Errorf("lastSeenAt = %v, want the row's last_seen_at %v", first.LastSeenAt, rows[phoneToken].LastSeenAt)
	}
	created := rows[phoneToken].CreatedAt

	// Refresh: same row, newer last_seen_at.
	second := s.register(t, sam, phoneToken, "ios")
	if !second.LastSeenAt.After(first.LastSeenAt) {
		t.Errorf("refresh lastSeenAt = %v, want after %v", second.LastSeenAt, first.LastSeenAt)
	}
	rows = s.tokenRows(t)
	if len(rows) != 1 {
		t.Fatalf("after refresh: %d rows, want 1", len(rows))
	}
	if r := rows[phoneToken]; r.UserID != samID || !r.CreatedAt.Equal(created) || !r.LastSeenAt.Equal(second.LastSeenAt) {
		t.Errorf("after refresh: row = %+v, want the same row with last_seen_at %v", r, second.LastSeenAt)
	}

	// Refresh with a changed platform updates it.
	s.register(t, sam, phoneToken, "android")
	if rows = s.tokenRows(t); len(rows) != 1 || rows[phoneToken].Platform != "android" {
		t.Errorf("after platform change: rows = %+v, want one android row", rows)
	}

	// A second device adds a second row.
	s.register(t, sam, tabletToken, "web")
	rows = s.tokenRows(t)
	if len(rows) != 2 || rows[tabletToken].UserID != samID || rows[tabletToken].Platform != "web" || rows[phoneToken].UserID != samID {
		t.Errorf("after second device: rows = %+v, want two rows for Sam", rows)
	}
}

// TestRegisterPushTokenMovesFromOtherUser: user B registers user A's token:
// one row, owned by B; A's other tokens are untouched.
func TestRegisterPushTokenMovesFromOtherUser(t *testing.T) {
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	bob := s.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	aliceID, bobID := s.userID(t, alice), s.userID(t, bob)

	s.register(t, alice, phoneToken, "ios")
	s.register(t, alice, tabletToken, "android")
	before := s.tokenRows(t)

	s.register(t, bob, phoneToken, "ios")
	rows := s.tokenRows(t)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2", rows)
	}
	if rows[phoneToken].UserID != bobID {
		t.Errorf("shared device token owner = %s, want Bob %s", rows[phoneToken].UserID, bobID)
	}
	if rows[tabletToken] != before[tabletToken] || rows[tabletToken].UserID != aliceID {
		t.Errorf("Alice's other token = %+v, want unchanged %+v", rows[tabletToken], before[tabletToken])
	}
}

func TestDeletePushToken(t *testing.T) {
	s := newMeServer(t)
	alice := s.verifier.Issue(auth.Token{UID: "alice", Name: "Alice"})
	bob := s.verifier.Issue(auth.Token{UID: "bob", Name: "Bob"})
	aliceID := s.userID(t, alice)

	s.register(t, alice, phoneToken, "ios")
	s.register(t, alice, tabletToken, "android")
	s.register(t, bob, laptopToken, "web")

	// Delete own token.
	s.deleteToken(t, alice, phoneToken)
	rows := s.tokenRows(t)
	if _, ok := rows[phoneToken]; ok || len(rows) != 2 || rows[tabletToken].UserID != aliceID {
		t.Errorf("after deleting own token: rows = %+v, want only the tablet and Bob's laptop", rows)
	}

	// Delete someone else's token: 204, still there.
	before := s.tokenRows(t)
	s.deleteToken(t, alice, laptopToken)
	if rows := s.tokenRows(t); len(rows) != 2 || rows[laptopToken] != before[laptopToken] {
		t.Errorf("after Alice deleted Bob's token: rows = %+v, want unchanged %+v", rows, before)
	}

	// Delete an unknown token, and one already deleted: 204.
	s.deleteToken(t, alice, "ExponentPushToken[never-registered]")
	s.deleteToken(t, alice, phoneToken)
	if rows := s.tokenRows(t); len(rows) != 2 {
		t.Errorf("after deleting unknown tokens: rows = %+v, want 2", rows)
	}

	// Unencoded brackets reach the same token.
	s.register(t, alice, phoneToken, "ios")
	resp, b := s.do(t, http.MethodDelete, "/me/push-tokens/"+phoneToken, alice, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE with raw brackets: status = %d (body %s)", resp.StatusCode, b)
	}
	if _, ok := s.tokenRows(t)[phoneToken]; ok {
		t.Error("DELETE with raw brackets left the token")
	}
}

func TestRegisterPushTokenValidation(t *testing.T) {
	s := newMeServer(t)
	sam := s.verifier.Issue(auth.Token{UID: "sam", Name: "Sam"})
	long := "ExponentPushToken[" + strings.Repeat("a", MaxPushTokenLength) + "]"

	tests := []struct {
		name string
		body string
		code string
	}{
		{"not an Expo token", `{"token":"abc","platform":"ios"}`, CodeInvalidToken},
		{"empty token", `{"token":"","platform":"ios"}`, CodeInvalidToken},
		{"missing token", `{"platform":"ios"}`, CodeInvalidToken},
		{"empty brackets", `{"token":"ExponentPushToken[]","platform":"ios"}`, CodeInvalidToken},
		{"too long", `{"token":"` + long + `","platform":"ios"}`, CodeInvalidToken},
		{"unknown platform", `{"token":"` + phoneToken + `","platform":"windows"}`, CodeInvalidPlatform},
		{"upper-case platform", `{"token":"` + phoneToken + `","platform":"IOS"}`, CodeInvalidPlatform},
		{"empty platform", `{"token":"` + phoneToken + `","platform":""}`, CodeInvalidPlatform},
		{"missing platform", `{"token":"` + phoneToken + `"}`, CodeInvalidPlatform},
		{"invalid JSON", `{"token":`, CodeBadRequest},
		{"token not a string", `{"token":1,"platform":"ios"}`, CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, b := s.do(t, http.MethodPost, "/me/push-tokens", sam, tt.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", resp.StatusCode, b)
			}
			var e struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(b, &e); err != nil {
				t.Fatalf("body %s: %v", b, err)
			}
			if e.Error.Code != tt.code || e.Error.Message == "" {
				t.Errorf("error = %+v, want code %s and a message", e.Error, tt.code)
			}
		})
	}

	// No body at all.
	resp, b := s.do(t, http.MethodPost, "/me/push-tokens", sam, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("no body: status = %d, want 400 (body %s)", resp.StatusCode, b)
	}

	if rows := s.tokenRows(t); len(rows) != 0 {
		t.Errorf("rows after invalid requests = %+v, want none", rows)
	}
}

func TestPushTokensRequireAuth(t *testing.T) {
	s := newMeServer(t)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/me/push-tokens", `{"token":"` + phoneToken + `","platform":"ios"}`},
		{http.MethodDelete, "/me/push-tokens/" + url.PathEscape(phoneToken), ""},
	} {
		resp, b := s.do(t, c.method, c.path, "", c.body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without a token: status = %d, want 401 (body %s)", c.method, c.path, resp.StatusCode, b)
		}
	}
}

// TestPushEnabledKeepsTokens: pushEnabled on, off, on, with the tokens
// unchanged throughout.
func TestPushEnabledKeepsTokens(t *testing.T) {
	s := newMeServer(t)
	sam := s.verifier.Issue(auth.Token{UID: "sam", Name: "Sam"})

	if u := s.user(t, http.MethodGet, sam, ""); u["pushEnabled"] != false {
		t.Fatalf("new user pushEnabled = %v, want false", u["pushEnabled"])
	}
	s.register(t, sam, phoneToken, "ios")
	s.register(t, sam, tabletToken, "android")
	tokens := s.tokenRows(t)

	for _, on := range []bool{true, false, true} {
		body := `{"pushEnabled":false}`
		if on {
			body = `{"pushEnabled":true}`
		}
		u := s.user(t, http.MethodPatch, sam, body)
		if u["pushEnabled"] != on {
			t.Errorf("PATCH %s returned pushEnabled %v", body, u["pushEnabled"])
		}
		if u := s.user(t, http.MethodGet, sam, ""); u["pushEnabled"] != on {
			t.Errorf("GET /me after PATCH %s: pushEnabled %v", body, u["pushEnabled"])
		}
		if got := s.tokenRows(t); len(got) != len(tokens) || got[phoneToken] != tokens[phoneToken] || got[tabletToken] != tokens[tabletToken] {
			t.Errorf("tokens after PATCH %s = %+v, want unchanged %+v", body, got, tokens)
		}
	}

	// Other fields leave pushEnabled alone, and pushEnabled leaves them alone.
	u := s.user(t, http.MethodPatch, sam, `{"timezone":"America/Toronto"}`)
	if u["pushEnabled"] != true || u["timezone"] != "America/Toronto" {
		t.Errorf("PATCH timezone = %v, want pushEnabled still true", u)
	}
	u = s.user(t, http.MethodPatch, sam, `{"pushEnabled":false}`)
	if u["timezone"] != "America/Toronto" || u["displayName"] != "Sam" || u["pushEnabled"] != false {
		t.Errorf("PATCH pushEnabled = %v, want other fields unchanged", u)
	}

	// pushEnabled is per user.
	other := s.verifier.Issue(auth.Token{UID: "other", Name: "Other"})
	s.user(t, http.MethodPatch, sam, `{"pushEnabled":true}`)
	if u := s.user(t, http.MethodGet, other, ""); u["pushEnabled"] != false {
		t.Errorf("another user's pushEnabled = %v, want false", u["pushEnabled"])
	}

	resp, b := s.call(t, http.MethodPatch, sam, `{"pushEnabled":"yes"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH pushEnabled \"yes\": status = %d, want 400 (body %s)", resp.StatusCode, b)
	}
}
