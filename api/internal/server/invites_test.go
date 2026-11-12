package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/auth/authtest"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// fakeAPI's invites endpoints, so it keeps satisfying StrictServerInterface.

func (fakeAPI) CreateInvite(context.Context, openapi.CreateInviteRequestObject) (openapi.CreateInviteResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) ListInvites(context.Context, openapi.ListInvitesRequestObject) (openapi.ListInvitesResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) RevokeInvite(context.Context, openapi.RevokeInviteRequestObject) (openapi.RevokeInviteResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) JoinInvite(context.Context, openapi.JoinInviteRequestObject) (openapi.JoinInviteResponseObject, error) {
	return nil, errNotImplemented
}

// invite is a decoded Invite response.
type invite struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// joinResult is a decoded JoinInviteResult response.
type joinResult struct {
	Household     *household `json:"household"`
	AlreadyMember *bool      `json:"alreadyMember"`
}

// invitesFixture is the real API on a fresh database: Home (named
// homeName) owned by owner with member m1, a second household Other also
// owned by owner, and an outsider who is in no household. logs holds
// everything the server logged.
type invitesFixture struct {
	*meServer
	api                 *API
	logs                *syncBuffer
	home, other         household
	owner, m1, outsider memberUser
	newUser             func(uid, name string) memberUser
}

const homeName = "Maple Street"

func newInvitesFixture(t *testing.T, opts ...Option) *invitesFixture {
	t.Helper()
	pool := dbtest.New(t)
	logger, logs := newLogger()
	v := authtest.NewVerifier()
	api := NewAPI(pool, logger, opts...)
	url := serve(t, NewHandler(api, auth.NewAuthenticator(v, pool), logger, nil))
	s := &meServer{url: url, pool: pool, verifier: v}
	f := &invitesFixture{meServer: s, api: api, logs: logs}
	f.newUser = func(uid, name string) memberUser {
		tok := s.verifier.Issue(auth.Token{UID: uid, Name: name, Email: uid + "@example.com"})
		return memberUser{token: tok, id: s.userID(t, tok)}
	}
	f.owner = f.newUser("alice", "Alice")
	f.m1 = f.newUser("bob", "Bob")
	f.outsider = f.newUser("dave", "Dave")
	f.home = s.create(t, f.owner.token, homeName)
	f.other = s.create(t, f.owner.token, "Other")
	f.exec(t, `INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'member')`, f.home.ID, f.m1.id)
	return f
}

func (f *invitesFixture) createInvite(t *testing.T, token, householdID string) invite {
	t.Helper()
	resp, b := f.req(t, http.MethodPost, "/households/"+householdID+"/invites", token, "")
	var inv invite
	wantStatus(t, resp, b, http.StatusCreated, &inv)
	return inv
}

func (f *invitesFixture) listInvites(t *testing.T, token, householdID string) []invite {
	t.Helper()
	resp, b := f.req(t, http.MethodGet, "/households/"+householdID+"/invites", token, "")
	var l struct {
		Invites *[]invite `json:"invites"`
	}
	wantStatus(t, resp, b, http.StatusOK, &l)
	if l.Invites == nil {
		t.Fatalf("GET invites = %s, want an invites array", b)
	}
	return *l.Invites
}

func (f *invitesFixture) revoke(t *testing.T, token, householdID, inviteID string) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodDelete, "/households/"+householdID+"/invites/"+inviteID, token, "")
}

// joinRaw posts body ("" for none) to /invites/join.
func (f *invitesFixture) joinRaw(t *testing.T, token, body string) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodPost, "/invites/join", token, body)
}

func (f *invitesFixture) join(t *testing.T, token, code string) (*http.Response, []byte) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code})
	return f.joinRaw(t, token, string(body))
}

// wantJoined checks a join with code returns 200 with the household, role
// and alreadyMember.
func (f *invitesFixture) wantJoined(t *testing.T, token, code, householdID, role string, already bool) {
	t.Helper()
	resp, b := f.join(t, token, code)
	var r joinResult
	wantStatus(t, resp, b, http.StatusOK, &r)
	if r.Household == nil || r.AlreadyMember == nil {
		t.Fatalf("join %q = %s, want household and alreadyMember", code, b)
	}
	if r.Household.ID != householdID || r.Household.Role != role || *r.AlreadyMember != already {
		t.Errorf("join %q = household %s role %s alreadyMember %v; want %s %s %v",
			code, r.Household.ID, r.Household.Role, *r.AlreadyMember, householdID, role, already)
	}
}

// wantJoinError checks a join with code fails with status and errCode and
// says nothing about Home.
func (f *invitesFixture) wantJoinError(t *testing.T, token, code string, status int, errCode string) {
	t.Helper()
	resp, b := f.join(t, token, code)
	wantError(t, resp, b, status, errCode)
	f.wantNoLeak(t, b)
}

// wantNoLeak checks an error body has only error.code and error.message, and
// mentions neither household's ID or name.
func (f *invitesFixture) wantNoLeak(t *testing.T, b []byte) {
	t.Helper()
	for _, s := range []string{f.home.ID, homeName, f.other.ID, f.owner.id, "Alice"} {
		if strings.Contains(string(b), s) {
			t.Errorf("error body %s contains %q", b, s)
		}
	}
	var raw map[string]map[string]any
	if err := json.Unmarshal(b, &raw); err != nil || len(raw) != 1 || len(raw["error"]) != 2 {
		t.Errorf("error body %s, want exactly {error: {code, message}}", b)
	}
}

// addInvite inserts an invite for householdID directly, expiring the given
// interval from now ("-1 second" for expired), revoked if revoked is true.
// It returns the invite ID.
func (f *invitesFixture) addInvite(t *testing.T, householdID, code, expiresIn string, revoked bool) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(), `
		INSERT INTO household_invites (household_id, code, created_by, expires_at, revoked_at)
		VALUES ($1, $2, $3, now() + $4::interval, CASE WHEN $5 THEN now() END)
		RETURNING id::text`, householdID, code, f.owner.id, expiresIn, revoked).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *invitesFixture) revokedAt(t *testing.T, inviteID string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := f.pool.QueryRow(context.Background(),
		`SELECT revoked_at FROM household_invites WHERE id = $1`, inviteID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func (f *invitesFixture) inviteCount(t *testing.T) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM household_invites`)
}

// membership returns userID's role and joined_at in householdID ("" if not a
// member).
func (f *invitesFixture) membership(t *testing.T, householdID, userID string) (string, time.Time) {
	t.Helper()
	var role string
	var at time.Time
	err := f.pool.QueryRow(context.Background(),
		`SELECT role::text, joined_at FROM household_members WHERE household_id = $1 AND user_id = $2`,
		householdID, userID).Scan(&role, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", at
	}
	if err != nil {
		t.Fatal(err)
	}
	return role, at
}

func TestNewInviteCodeFormat(t *testing.T) {
	seen := map[rune]bool{}
	for range 1000 {
		code, err := NewInviteCode()
		if err != nil {
			t.Fatal(err)
		}
		if !inviteCodePattern.MatchString(code) {
			t.Fatalf("code %q does not match %s", code, inviteCodePattern)
		}
		for _, r := range code {
			seen[r] = true
		}
	}
	// 8000 draws from 31 characters: every one turns up.
	if len(seen) != len(InviteCodeAlphabet) {
		t.Errorf("1000 codes used %d distinct characters, want all %d", len(seen), len(InviteCodeAlphabet))
	}
	if len(InviteCodeAlphabet) != 31 || strings.ContainsAny(InviteCodeAlphabet, "ILO01") {
		t.Errorf("alphabet %q, want 31 characters without I, L, O, 0, 1", InviteCodeAlphabet)
	}
}

func TestNormalizeInviteCode(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ABCDEFGH", "ABCDEFGH"},
		{"abcd-efgh", "ABCDEFGH"},
		{" ABCD EFGH ", "ABCDEFGH"},
		{"AbCd-EfGh", "ABCDEFGH"},
		{"\tab-cd\n ef--gh\r", "ABCDEFGH"},
		{"OOOO0000", "OOOO0000"}, // no O/0 substitution
		{" - ", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeInviteCode(tt.in); got != tt.want {
			t.Errorf("NormalizeInviteCode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	for code, ok := range map[string]bool{
		"ABCDEFGH": true, "K7MQ3XPA": true, "23456789": true,
		"ABCDEFG": false, "ABCDEFGHJ": false, "ABCDEFGI": false, "ABCDEFGL": false,
		"ABCDEFGO": false, "ABCDEFG0": false, "ABCDEFG1": false, "abcdefgh": false,
	} {
		if ValidInviteCode(code) != ok {
			t.Errorf("ValidInviteCode(%q) = %v, want %v", code, !ok, ok)
		}
	}
}

func TestCreateInvite(t *testing.T) {
	f := newInvitesFixture(t)

	resp, b := f.req(t, http.MethodPost, "/households/"+f.home.ID+"/invites", f.owner.token, "")
	var raw map[string]any
	wantStatus(t, resp, b, http.StatusCreated, &raw)
	if len(raw) != 5 {
		t.Errorf("invite %s, want exactly id, code, url, expiresAt, createdAt", b)
	}
	var inv invite
	if err := json.Unmarshal(b, &inv); err != nil {
		t.Fatal(err)
	}
	if !inviteCodePattern.MatchString(inv.Code) {
		t.Errorf("code %q does not match %s", inv.Code, inviteCodePattern)
	}
	if want := "http://localhost:8081/join/" + inv.Code; inv.URL != want {
		t.Errorf("url = %q, want %q (the default APP_BASE_URL)", inv.URL, want)
	}
	if d := inv.ExpiresAt.Sub(inv.CreatedAt); d != 7*24*time.Hour {
		t.Errorf("expiresAt - createdAt = %v, want 168h", d)
	}
	if time.Since(inv.CreatedAt).Abs() > time.Minute {
		t.Errorf("createdAt = %v, want about now", inv.CreatedAt)
	}

	var household, createdBy string
	var revoked *time.Time
	if err := f.pool.QueryRow(context.Background(),
		`SELECT household_id::text, created_by::text, revoked_at FROM household_invites WHERE id = $1 AND code = $2`,
		inv.ID, inv.Code).Scan(&household, &createdBy, &revoked); err != nil {
		t.Fatalf("invite row: %v", err)
	}
	if household != f.home.ID || createdBy != f.owner.id || revoked != nil {
		t.Errorf("row household %s created_by %s revoked_at %v; want %s %s nil", household, createdBy, revoked, f.home.ID, f.owner.id)
	}

	// Creating another does not revoke the first.
	second := f.createInvite(t, f.owner.token, f.home.ID)
	if second.Code == inv.Code || second.ID == inv.ID {
		t.Errorf("second invite %+v repeats the first %+v", second, inv)
	}
	if got := f.listInvites(t, f.owner.token, f.home.ID); len(got) != 2 || got[0].ID != second.ID || got[1].ID != inv.ID {
		t.Errorf("invites = %+v, want the second then the first", got)
	}
}

func TestCreateInviteAppBaseURL(t *testing.T) {
	for _, base := range []string{"https://x.web.app", "https://x.web.app/"} {
		t.Run(base, func(t *testing.T) {
			f := newInvitesFixture(t, WithAppBaseURL(base))
			inv := f.createInvite(t, f.owner.token, f.home.ID)
			if want := "https://x.web.app/join/" + inv.Code; inv.URL != want {
				t.Errorf("url = %q, want %q", inv.URL, want)
			}
			if got := f.listInvites(t, f.owner.token, f.home.ID); len(got) != 1 || got[0].URL != inv.URL {
				t.Errorf("listed invites = %+v, want url %q", got, inv.URL)
			}
		})
	}
}

// codes returns a generator that returns each of codes in turn and counts
// its calls.
func codes(calls *int, codes ...string) func() (string, error) {
	return func() (string, error) {
		c := codes[*calls%len(codes)]
		*calls++
		return c, nil
	}
}

func TestCreateInviteCollisionRetry(t *testing.T) {
	f := newInvitesFixture(t)
	// Taken codes: an active, an expired and a revoked invite, in another
	// household, so the conflict is invisible to the caller under RLS too.
	f.addInvite(t, f.other.ID, "AAAAAAAA", "7 days", false)
	f.addInvite(t, f.other.ID, "BBBBBBBB", "-1 second", false)
	f.addInvite(t, f.other.ID, "CCCCCCCC", "7 days", true)

	for i, taken := range []string{"AAAAAAAA", "BBBBBBBB", "CCCCCCCC"} {
		fresh := []string{"DDDDDDDD", "EEEEEEEE", "FFFFFFFF"}[i]
		calls := 0
		f.api.newInviteCode = codes(&calls, taken, fresh)
		inv := f.createInvite(t, f.owner.token, f.home.ID)
		if inv.Code != fresh || calls != 2 {
			t.Errorf("with %s taken: code %q after %d attempts, want %q after 2", taken, inv.Code, calls, fresh)
		}
	}
	// A code taken by this household's own invite is retried too.
	calls := 0
	f.api.newInviteCode = codes(&calls, "DDDDDDDD", "GGGGGGGG")
	if inv := f.createInvite(t, f.owner.token, f.home.ID); inv.Code != "GGGGGGGG" {
		t.Errorf("code = %q, want GGGGGGGG", inv.Code)
	}

	// Every attempt collides: 500 after exactly 5 attempts, nothing created.
	before := f.inviteCount(t)
	calls = 0
	f.api.newInviteCode = codes(&calls, "AAAAAAAA", "BBBBBBBB", "CCCCCCCC")
	resp, b := f.req(t, http.MethodPost, "/households/"+f.home.ID+"/invites", f.owner.token, "")
	wantError(t, resp, b, http.StatusInternalServerError, CodeInternal)
	if calls != 5 {
		t.Errorf("%d attempts, want 5", calls)
	}
	if n := f.inviteCount(t); n != before {
		t.Errorf("%d invites after the failed create, want %d", n, before)
	}
}

func TestInviteEndpointsOwnerOnly(t *testing.T) {
	f := newInvitesFixture(t)
	inv := f.createInvite(t, f.owner.token, f.home.ID)
	missing := "0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"
	before := f.inviteCount(t)

	type call struct{ method, path string }
	calls := func(householdID, inviteID string) []call {
		return []call{
			{http.MethodPost, "/households/" + householdID + "/invites"},
			{http.MethodGet, "/households/" + householdID + "/invites"},
			{http.MethodDelete, "/households/" + householdID + "/invites/" + inviteID},
		}
	}
	// A member gets 403 owner_only.
	for _, c := range calls(f.home.ID, inv.ID) {
		resp, b := f.req(t, c.method, c.path, f.m1.token, "")
		wantError(t, resp, b, http.StatusForbidden, CodeOwnerOnly)
	}
	// An outsider gets 404, for Home and for a household that does not
	// exist; so does the owner for a household that does not exist.
	for _, u := range []memberUser{f.outsider, f.owner} {
		for _, id := range []string{f.home.ID, missing} {
			if u == f.owner && id == f.home.ID {
				continue
			}
			for _, c := range calls(id, inv.ID) {
				resp, b := f.req(t, c.method, c.path, u.token, "")
				wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
			}
		}
	}
	// Malformed IDs get 400.
	for _, c := range append(calls("not-a-uuid", inv.ID), call{http.MethodDelete, "/households/" + f.home.ID + "/invites/not-a-uuid"}) {
		resp, b := f.req(t, c.method, c.path, f.owner.token, "")
		wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)
	}

	// Nothing changed.
	if n := f.inviteCount(t); n != before {
		t.Errorf("%d invites, want %d", n, before)
	}
	if at := f.revokedAt(t, inv.ID); at != nil {
		t.Errorf("invite revoked at %v by a non-owner", at)
	}
}

func TestListInvites(t *testing.T) {
	f := newInvitesFixture(t)

	// None yet: an empty list, not 404.
	resp, b := f.req(t, http.MethodGet, "/households/"+f.home.ID+"/invites", f.owner.token, "")
	wantStatus(t, resp, b, http.StatusOK, nil)
	if got := strings.TrimSpace(string(b)); got != `{"invites":[]}` {
		t.Errorf("empty list = %s, want {\"invites\":[]}", got)
	}

	older := f.addInvite(t, f.home.ID, "HHHHHHHH", "6 days", false)
	newer := f.addInvite(t, f.home.ID, "JJJJJJJJ", "7 days", false)
	f.exec(t, `UPDATE household_invites SET created_at = now() - interval '1 day' WHERE id = $1`, older)
	f.addInvite(t, f.home.ID, "KKKKKKKK", "7 days", true)     // revoked
	f.addInvite(t, f.home.ID, "MMMMMMMM", "-1 second", false) // expired
	f.addInvite(t, f.other.ID, "NNNNNNNN", "7 days", false)   // another household
	exact := f.addInvite(t, f.home.ID, "PPPPPPPP", "1 day", false)
	f.exec(t, `UPDATE household_invites SET expires_at = now() WHERE id = $1`, exact) // expires_at <= now()

	got := f.listInvites(t, f.owner.token, f.home.ID)
	if len(got) != 2 || got[0].ID != newer || got[1].ID != older {
		t.Fatalf("invites = %+v, want JJJJJJJJ then HHHHHHHH", got)
	}
	if got[0].Code != "JJJJJJJJ" || got[0].URL != "http://localhost:8081/join/JJJJJJJJ" {
		t.Errorf("newest = %+v", got[0])
	}

	// Equal created_at: the higher ID first.
	f.exec(t, `UPDATE household_invites SET created_at = '2026-01-01T00:00:00Z' WHERE id IN ($1, $2)`, older, newer)
	got = f.listInvites(t, f.owner.token, f.home.ID)
	wantFirst := older
	if newer > older {
		wantFirst = newer
	}
	if len(got) != 2 || got[0].ID != wantFirst {
		t.Errorf("tied invites = %+v, want %s first", got, wantFirst)
	}

	// Other has an active invite; a household with only inactive ones is
	// empty.
	if got := f.listInvites(t, f.owner.token, f.other.ID); len(got) != 1 {
		t.Errorf("Other's invites = %+v, want 1", got)
	}
	f.exec(t, `UPDATE household_invites SET revoked_at = now() WHERE household_id = $1`, f.other.ID)
	if got := f.listInvites(t, f.owner.token, f.other.ID); len(got) != 0 {
		t.Errorf("Other's invites after revoking = %+v, want none", got)
	}
}

func TestRevokeInvite(t *testing.T) {
	f := newInvitesFixture(t)
	inv := f.createInvite(t, f.owner.token, f.home.ID)
	keep := f.createInvite(t, f.owner.token, f.home.ID)

	resp, b := f.revoke(t, f.owner.token, f.home.ID, inv.ID)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	if len(b) != 0 {
		t.Errorf("DELETE body = %q, want empty", b)
	}
	if got := f.listInvites(t, f.owner.token, f.home.ID); len(got) != 1 || got[0].ID != keep.ID {
		t.Errorf("invites after revoking = %+v, want only %s", got, keep.ID)
	}
	f.wantJoinError(t, f.outsider.token, inv.Code, http.StatusGone, CodeInviteRevoked)
	first := f.revokedAt(t, inv.ID)
	if first == nil {
		t.Fatal("revoked_at not set")
	}

	// Again: 204, revoked_at unchanged.
	resp, b = f.revoke(t, f.owner.token, f.home.ID, inv.ID)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	if again := f.revokedAt(t, inv.ID); again == nil || !again.Equal(*first) {
		t.Errorf("revoked_at after revoking twice = %v, want %v", again, first)
	}

	// An expired invite: 204 and revoked_at set.
	expired := f.addInvite(t, f.home.ID, "QQQQQQQQ", "-1 hour", false)
	resp, b = f.revoke(t, f.owner.token, f.home.ID, expired)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	if f.revokedAt(t, expired) == nil {
		t.Error("expired invite's revoked_at not set")
	}

	// Another household's invite (one the caller also owns), through Home:
	// 404 and not revoked. An ID that is no invite: 404.
	otherInv := f.createInvite(t, f.owner.token, f.other.ID)
	for _, id := range []string{otherInv.ID, "0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"} {
		resp, b = f.revoke(t, f.owner.token, f.home.ID, id)
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	}
	if at := f.revokedAt(t, otherInv.ID); at != nil {
		t.Errorf("Other's invite revoked at %v through Home", at)
	}
	if got := f.listInvites(t, f.owner.token, f.other.ID); len(got) != 1 {
		t.Errorf("Other's invites = %+v, want 1", got)
	}
	// Members who joined with a revoked code stay.
	f.wantJoined(t, f.outsider.token, keep.Code, f.home.ID, "member", false)
	resp, b = f.revoke(t, f.owner.token, f.home.ID, keep.ID)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	if role, _ := f.membership(t, f.home.ID, f.outsider.id); role != "member" {
		t.Errorf("outsider's role after revoking = %q, want member", role)
	}
}

func TestJoinInvite(t *testing.T) {
	f := newInvitesFixture(t)
	inv := f.createInvite(t, f.owner.token, f.home.ID)

	resp, b := f.join(t, f.outsider.token, inv.Code)
	var raw map[string]any
	wantStatus(t, resp, b, http.StatusOK, &raw)
	if len(raw) != 2 {
		t.Errorf("join result %s, want exactly household and alreadyMember", b)
	}
	var r joinResult
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Household == nil || r.AlreadyMember == nil || *r.AlreadyMember {
		t.Fatalf("join = %s, want alreadyMember false", b)
	}
	if h := *r.Household; h.ID != f.home.ID || h.Name != homeName || h.Role != "member" || !h.CreatedAt.Equal(f.home.CreatedAt) {
		t.Errorf("household = %+v, want %+v with role member", h, f.home)
	}
	if role, _ := f.membership(t, f.home.ID, f.outsider.id); role != "member" {
		t.Errorf("outsider's role = %q, want member", role)
	}
	// The invite is unchanged and still listed.
	if got := f.listInvites(t, f.owner.token, f.home.ID); len(got) != 1 || got[0] != inv {
		t.Errorf("invites after a join = %+v, want [%+v]", got, inv)
	}

	// After joining, the household is in GET /households, GET
	// /households/{id} and the members list.
	listed := false
	for _, h := range f.list(t, f.outsider.token) {
		if h.ID == f.home.ID {
			listed = h.Role == "member"
		}
	}
	if !listed {
		t.Error("GET /households does not list Home with role member")
	}
	resp, b = f.req(t, http.MethodGet, "/households/"+f.home.ID, f.outsider.token, "")
	var h household
	wantStatus(t, resp, b, http.StatusOK, &h)
	if h.Role != "member" {
		t.Errorf("GET /households/{id} role = %q, want member", h.Role)
	}
	mf := &membersFixture{meServer: f.meServer}
	found := false
	for _, m := range mf.members(t, f.outsider.token, f.home.ID) {
		if m.UserID == f.outsider.id {
			found = m.Role == "member"
		}
	}
	if !found {
		t.Error("the members list does not include the outsider as a member")
	}
}

func TestJoinInviteNormalisesCode(t *testing.T) {
	f := newInvitesFixture(t)
	f.addInvite(t, f.home.ID, "ABCDEFGH", "7 days", false)

	carol := f.newUser("carol", "Carol")
	erin := f.newUser("erin", "Erin")
	frank := f.newUser("frank", "Frank")
	f.wantJoined(t, carol.token, "abcd-efgh", f.home.ID, "member", false)
	f.wantJoined(t, erin.token, " ABCD EFGH ", f.home.ID, "member", false)
	f.wantJoined(t, frank.token, "AbCd-EfGh", f.home.ID, "member", false)
	f.wantJoined(t, f.outsider.token, " ab cd -\tef gh\n", f.home.ID, "member", false)
}

func TestJoinInviteErrors(t *testing.T) {
	f := newInvitesFixture(t)
	f.addInvite(t, f.home.ID, "EXPRDABC", "-1 second", false)
	f.addInvite(t, f.home.ID, "RVKDABCD", "7 days", true)
	f.addInvite(t, f.home.ID, "BTHABCDE", "-1 day", true)
	exact := f.addInvite(t, f.home.ID, "XACTABCD", "1 day", false)
	f.exec(t, `UPDATE household_invites SET expires_at = now() WHERE id = $1`, exact)
	before := f.count(t, `SELECT count(*) FROM household_members`)

	// Unknown and malformed codes: 404 invite_not_found.
	for _, code := range []string{"ZZZZZZZZ", "ABCDEFG", "ABCDEFGHJ", "OOOOOOOO", "ABCDEFG1", "ABCD!FGH", "ÄBCDEFGH"} {
		f.wantJoinError(t, f.outsider.token, code, http.StatusNotFound, CodeInviteNotFound)
	}
	// Revoked, expired, and both (revoked wins).
	f.wantJoinError(t, f.outsider.token, "RVKDABCD", http.StatusGone, CodeInviteRevoked)
	f.wantJoinError(t, f.outsider.token, "EXPRDABC", http.StatusGone, CodeInviteExpired)
	f.wantJoinError(t, f.outsider.token, "XACTABCD", http.StatusGone, CodeInviteExpired)
	f.wantJoinError(t, f.outsider.token, "bthabcde", http.StatusGone, CodeInviteRevoked)

	// Bad requests: 400 bad_request.
	for _, body := range []string{"", "{", "not json", `{}`, `{"code":""}`, `{"code":"  - - "}`, `{"code":null}`, `{"code":12345678}`} {
		resp, b := f.joinRaw(t, f.outsider.token, body)
		wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)
	}

	if n := f.count(t, `SELECT count(*) FROM household_members`); n != before {
		t.Errorf("%d memberships after failed joins, want %d", n, before)
	}
	if role, _ := f.membership(t, f.home.ID, f.outsider.id); role != "" {
		t.Errorf("outsider's role = %q after failed joins, want none", role)
	}
}

// failingPool is a Pool whose every call fails, to show a request never
// reaches the database.
type failingPool struct{ t *testing.T }

var errUnexpectedQuery = errors.New("unexpected database call")

func (p failingPool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	p.t.Error("unexpected Exec")
	return pgconn.CommandTag{}, errUnexpectedQuery
}

func (p failingPool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	p.t.Error("unexpected Query")
	return nil, errUnexpectedQuery
}

func (p failingPool) QueryRow(context.Context, string, ...any) pgx.Row {
	p.t.Error("unexpected QueryRow")
	return nil
}

func (p failingPool) Begin(context.Context) (pgx.Tx, error) {
	p.t.Error("unexpected Begin")
	return nil, errUnexpectedQuery
}

// TestJoinInviteMalformedCodeSkipsDatabase checks bad and malformed codes
// are answered without touching the database.
func TestJoinInviteMalformedCodeSkipsDatabase(t *testing.T) {
	pool := dbtest.New(t) // for authentication only
	logger, _ := newLogger()
	v := authtest.NewVerifier()
	url := serve(t, NewHandler(NewAPI(failingPool{t}, logger), auth.NewAuthenticator(v, pool), logger, nil))
	s := &meServer{url: url, pool: pool, verifier: v}
	tok := v.Issue(auth.Token{UID: "sam", Name: "Sam"})

	for _, code := range []string{"ABCDEFG", "OOOOOOOO", "abcd-efg1"} {
		resp, b := s.req(t, http.MethodPost, "/invites/join", tok, `{"code":"`+code+`"}`)
		wantError(t, resp, b, http.StatusNotFound, CodeInviteNotFound)
	}
	for _, body := range []string{"", `{}`, `{"code":" - "}`} {
		resp, b := s.req(t, http.MethodPost, "/invites/join", tok, body)
		wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)
	}
}

func TestJoinInviteAlreadyMember(t *testing.T) {
	f := newInvitesFixture(t)
	inv := f.createInvite(t, f.owner.token, f.home.ID)
	f.addInvite(t, f.home.ID, "EXPRDABC", "-1 second", false)
	f.addInvite(t, f.home.ID, "RVKDABCD", "7 days", true)
	ownerRole, ownerJoined := f.membership(t, f.home.ID, f.owner.id)
	m1Role, m1Joined := f.membership(t, f.home.ID, f.m1.id)
	before := f.count(t, `SELECT count(*) FROM household_members`)

	// Valid, expired and revoked codes alike.
	for _, code := range []string{inv.Code, "exprd-abc", "RVKDABCD"} {
		f.wantJoined(t, f.m1.token, code, f.home.ID, "member", true)
		f.wantJoined(t, f.owner.token, code, f.home.ID, "owner", true)
	}

	if n := f.count(t, `SELECT count(*) FROM household_members`); n != before {
		t.Errorf("%d memberships, want %d", n, before)
	}
	if role, at := f.membership(t, f.home.ID, f.owner.id); role != ownerRole || !at.Equal(ownerJoined) {
		t.Errorf("owner is %s since %v, want %s since %v", role, at, ownerRole, ownerJoined)
	}
	if role, at := f.membership(t, f.home.ID, f.m1.id); role != m1Role || !at.Equal(m1Joined) {
		t.Errorf("member is %s since %v, want %s since %v", role, at, m1Role, m1Joined)
	}
}

func TestJoinInviteSharedAndScoped(t *testing.T) {
	f := newInvitesFixture(t)
	inv := f.createInvite(t, f.owner.token, f.home.ID)
	otherInv := f.createInvite(t, f.owner.token, f.other.ID)

	// Two people use the same code; both are members.
	carol := f.newUser("carol", "Carol")
	f.wantJoined(t, f.outsider.token, inv.Code, f.home.ID, "member", false)
	f.wantJoined(t, carol.token, inv.Code, f.home.ID, "member", false)
	for _, u := range []memberUser{f.outsider, carol} {
		if role, _ := f.membership(t, f.home.ID, u.id); role != "member" {
			t.Errorf("%s's role = %q, want member", u.id, role)
		}
	}

	// Home's code never adds anyone to Other, and the reverse.
	for _, u := range []memberUser{f.outsider, carol} {
		if role, _ := f.membership(t, f.other.ID, u.id); role != "" {
			t.Errorf("%s is a %s of Other after using Home's code", u.id, role)
		}
	}
	f.wantJoined(t, f.m1.token, otherInv.Code, f.other.ID, "member", false)
	if n := f.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1`, f.home.ID); n != 4 {
		t.Errorf("Home has %d members, want 4", n)
	}
}

func TestJoinInviteAfterRemoval(t *testing.T) {
	f := newInvitesFixture(t)
	inv := f.createInvite(t, f.owner.token, f.home.ID)

	// Removed by the owner, then rejoins.
	resp, b := f.req(t, http.MethodDelete, "/households/"+f.home.ID+"/members/"+f.m1.id, f.owner.token, "")
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	f.wantJoined(t, f.m1.token, inv.Code, f.home.ID, "member", false)

	// Leaves, then rejoins.
	resp, b = f.req(t, http.MethodDelete, "/households/"+f.home.ID+"/members/"+f.m1.id, f.m1.token, "")
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	f.wantJoined(t, f.m1.token, inv.Code, f.home.ID, "member", false)
}

func TestJoinInviteConcurrentSameUser(t *testing.T) {
	f := newInvitesFixture(t)
	inv := f.createInvite(t, f.owner.token, f.home.ID)

	for round := range 5 {
		u := f.newUser("racer"+string(rune('a'+round)), "Racer")
		type result struct {
			status  int
			already *bool
			body    []byte
		}
		results := make([]result, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				resp, b := f.join(t, u.token, inv.Code)
				var r joinResult
				_ = json.Unmarshal(b, &r)
				results[i] = result{resp.StatusCode, r.AlreadyMember, b}
			}()
		}
		close(start)
		wg.Wait()

		joined, already := 0, 0
		for _, r := range results {
			if r.status != http.StatusOK || r.already == nil {
				t.Fatalf("concurrent join: %d %s, want 200", r.status, r.body)
			}
			if *r.already {
				already++
			} else {
				joined++
			}
		}
		if joined != 1 || already != 1 {
			t.Errorf("concurrent joins: %d joined, %d already members; want 1 and 1", joined, already)
		}
		if n := f.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1 AND user_id = $2`, f.home.ID, u.id); n != 1 {
			t.Errorf("%d rows for the user, want 1", n)
		}
	}
}

// TestInviteCodesNotLogged checks the server's logs never contain a code, for
// successful and failed joins and for create and list.
func TestInviteCodesNotLogged(t *testing.T) {
	f := newInvitesFixture(t)
	inv := f.createInvite(t, f.owner.token, f.home.ID)
	f.listInvites(t, f.owner.token, f.home.ID)
	f.wantJoined(t, f.outsider.token, inv.Code, f.home.ID, "member", false)
	f.wantJoined(t, f.outsider.token, strings.ToLower(inv.Code), f.home.ID, "member", true)
	f.addInvite(t, f.home.ID, "RVKDABCD", "7 days", true)
	f.addInvite(t, f.home.ID, "EXPRDABC", "-1 second", false)
	carol := f.newUser("carol", "Carol")
	f.wantJoinError(t, carol.token, "ZZZZZZZZ", http.StatusNotFound, CodeInviteNotFound)
	f.wantJoinError(t, carol.token, "QQQQQQQ", http.StatusNotFound, CodeInviteNotFound)
	f.wantJoinError(t, carol.token, "RVKD-ABCD", http.StatusGone, CodeInviteRevoked)
	f.wantJoinError(t, carol.token, "EXPRDABC", http.StatusGone, CodeInviteExpired)

	logs := f.logs.String()
	if !strings.Contains(logs, "/invites/join") {
		t.Fatalf("no request lines logged for /invites/join: %s", logs)
	}
	for _, code := range []string{inv.Code, strings.ToLower(inv.Code), "ZZZZZZZZ", "QQQQQQQ", "RVKDABCD", "RVKD-ABCD", "EXPRDABC"} {
		if strings.Contains(logs, code) {
			t.Errorf("logs contain the code %q:\n%s", code, logs)
		}
	}
}

func TestInvitesRequireAuth(t *testing.T) {
	s := newMeServer(t)
	const id = "0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, "/households/" + id + "/invites"},
		{http.MethodGet, "/households/" + id + "/invites"},
		{http.MethodDelete, "/households/" + id + "/invites/" + id},
		{http.MethodPost, "/invites/join"},
	} {
		resp, b := s.req(t, r.method, r.path, "not-issued", "")
		wantError(t, resp, b, http.StatusUnauthorized, CodeUnauthorized)
	}
}
