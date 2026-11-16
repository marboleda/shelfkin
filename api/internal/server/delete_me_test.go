package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/auth/authtest"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// fakeDeleter is an auth.AccountDeleter that records the Firebase UIDs it
// deletes and fails with err when it is set.
type fakeDeleter struct {
	mu      sync.Mutex
	err     error
	deleted []string
}

func (d *fakeDeleter) DeleteUser(_ context.Context, uid string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	d.deleted = append(d.deleted, uid)
	return nil
}

func (d *fakeDeleter) setErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.err = err
}

func (d *fakeDeleter) calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.deleted)
}

// deleteFixture is an entryFixture (Sam owns Home, Alex is a member) whose
// API deletes Firebase users with a fakeDeleter.
type deleteFixture struct {
	*entryFixture
	deleter *fakeDeleter
}

func newDeleteFixture(t *testing.T) *deleteFixture {
	t.Helper()
	pool := dbtest.New(t)
	logger, _ := newLogger()
	v := authtest.NewVerifier()
	d := &fakeDeleter{}
	api := NewAPI(pool, logger, WithAccountDeleter(d))
	api.now = func() time.Time { return fixedNow }
	s := &meServer{url: serve(t, NewHandler(api, auth.NewAuthenticator(v, pool), logger, nil)), pool: pool, verifier: v}

	f := &entryFixture{meServer: s}
	f.sam = s.verifier.Issue(auth.Token{UID: "sam", Name: "Sam"})
	f.alex = s.verifier.Issue(auth.Token{UID: "alex", Name: "Alex"})
	f.samID = s.userID(t, f.sam)
	f.alexID = s.userID(t, f.alex)
	f.home = s.create(t, f.sam, "Home").ID
	s.exec(t, `INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'member')`, f.home, f.alexID)
	f.milk = f.builtin(t, "Milk")
	f.salt = f.builtin(t, "Salt")
	return &deleteFixture{entryFixture: f, deleter: d}
}

func (f *deleteFixture) deleteMe(t *testing.T, token string) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodDelete, "/me", token, "")
}

func (f *deleteFixture) registerToken(t *testing.T, token, expoToken string) {
	t.Helper()
	resp, b := f.req(t, http.MethodPost, "/me/push-tokens", token, `{"token":"`+expoToken+`","platform":"ios"}`)
	wantStatus(t, resp, b, http.StatusOK, nil)
}

// newUser signs in a new user (creating their users row) and returns their
// token and ID.
func (f *deleteFixture) newUser(t *testing.T, uid, name string) (string, string) {
	t.Helper()
	tok := f.verifier.Issue(auth.Token{UID: uid, Name: name})
	return tok, f.userID(t, tok)
}

// wantAccountGone checks that the user's row, memberships and push tokens are
// gone, that the Firebase user was deleted, and that their old token neither
// works nor recreates the account.
func (f *deleteFixture) wantAccountGone(t *testing.T, token, userID, firebaseUID string) {
	t.Helper()
	if n := f.count(t, `SELECT count(*) FROM users WHERE id = $1 OR firebase_uid = $2`, userID, firebaseUID); n != 0 {
		t.Errorf("%d users rows left for %s", n, firebaseUID)
	}
	if n := f.count(t, `SELECT count(*) FROM household_members WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("%d household_members rows left for %s", n, firebaseUID)
	}
	if n := f.count(t, `SELECT count(*) FROM push_tokens WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("%d push_tokens rows left for %s", n, firebaseUID)
	}
	if !slices.Contains(f.deleter.calls(), firebaseUID) {
		t.Errorf("Firebase deletions = %v, want %s among them", f.deleter.calls(), firebaseUID)
	}

	// The old token is still valid for Firebase (it has not expired) but is
	// refused, on any endpoint, and no new users row appears.
	for _, r := range []struct{ method, path, body string }{
		{http.MethodGet, "/me", ""},
		{http.MethodGet, "/households", ""},
		{http.MethodPost, "/households", `{"name":"Again"}`},
		{http.MethodDelete, "/me", ""},
	} {
		resp, b := f.req(t, r.method, r.path, token, r.body)
		wantError(t, resp, b, http.StatusUnauthorized, CodeUnauthorized)
	}
	if n := f.count(t, `SELECT count(*) FROM users WHERE firebase_uid = $1`, firebaseUID); n != 0 {
		t.Errorf("the old token recreated the account (%d rows)", n)
	}
}

// TestDeleteMePlainMember: a member who created and changed entries, created
// a custom item and has push tokens deletes their account. Their rows go;
// the household, its entries, events and items stay with a NULL actor.
func TestDeleteMePlainMember(t *testing.T) {
	f := newDeleteFixture(t)
	f.registerToken(t, f.alex, "ExponentPushToken[alex-phone]")
	f.registerToken(t, f.alex, "ExponentPushToken[alex-tablet]")
	f.registerToken(t, f.sam, "ExponentPushToken[sam-phone]")

	// Alex adds a custom item and an entry, and changes one of Sam's.
	custom := f.customItem(t, f.home, "Sourdough starter")
	f.exec(t, `UPDATE catalog_items SET created_by = $1 WHERE id = $2`, f.alexID, custom)
	alexEntry := newID()
	alexCreated := newID()
	resp, b := f.post(t, f.alex, f.home, map[string]any{"id": alexEntry, "eventId": alexCreated, "catalogItemId": custom})
	wantStatus(t, resp, b, http.StatusCreated, nil)
	samEntry, _ := f.createEntry(t, f.milk, nil)
	alexChanged := newID()
	f.update(t, f.alex, samEntry.ID, map[string]any{"eventId": alexChanged, "level": "low"})
	f.exec(t, `INSERT INTO household_item_defaults (household_id, catalog_item_id, location_id, updated_by)
		VALUES ($1, $2, 2, $3)`, f.home, f.salt, f.alexID)

	resp, b = f.deleteMe(t, f.alex)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	if len(b) != 0 {
		t.Errorf("204 body = %s, want empty", b)
	}
	if got := f.deleter.calls(); !slices.Equal(got, []string{"alex"}) {
		t.Errorf("Firebase deletions = %v, want [alex]", got)
	}
	f.wantAccountGone(t, f.alex, f.alexID, "alex")

	// Sam's rows are untouched.
	if n := f.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1 AND user_id = $2 AND role = 'owner'`, f.home, f.samID); n != 1 {
		t.Errorf("Sam's owner row: %d, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM push_tokens WHERE user_id = $1`, f.samID); n != 1 {
		t.Errorf("Sam's push tokens: %d, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM households WHERE id = $1`, f.home); n != 1 {
		t.Errorf("Home: %d rows, want 1", n)
	}

	// Alex's entry, events and custom item stay, with no creator or actor.
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1 AND created_by IS NULL AND updated_by IS NULL`, alexEntry); n != 1 {
		t.Errorf("Alex's entry: want it kept with NULL created_by and updated_by")
	}
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1 AND created_by = $2 AND updated_by IS NULL`, samEntry.ID, f.samID); n != 1 {
		t.Errorf("Sam's entry changed by Alex: want created_by Sam and updated_by NULL")
	}
	for _, ev := range []string{alexCreated, alexChanged} {
		if e := f.event(t, ev); e.ActorID != "" {
			t.Errorf("event %s: actor_id = %s, want NULL", ev, e.ActorID)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM catalog_items WHERE id = $1 AND created_by IS NULL`, custom); n != 1 {
		t.Errorf("Alex's custom item: want it kept with NULL created_by")
	}
	if n := f.count(t, `SELECT count(*) FROM household_item_defaults WHERE household_id = $1 AND updated_by IS NULL`, f.home); n != 1 {
		t.Errorf("defaults Alex set: want them kept with NULL updated_by")
	}

	// The pantry read returns a null actor for Alex's changes.
	p := f.pantry(t, f.sam, f.home, "")
	for _, id := range []string{alexEntry, samEntry.ID} {
		lc := p.entry(t, id).LastChange
		if lc == nil || lc.ActorID != nil || lc.ActorName != nil {
			t.Errorf("entry %s: lastChange = %+v, want a null actorId and actorName", id, lc)
		}
	}
}

// TestDeleteMeSoleOwner: the owner of a one-person household deletes their
// account. That household goes with everything in it; a household they are a
// member of stays, without them.
func TestDeleteMeSoleOwner(t *testing.T) {
	f := newDeleteFixture(t)
	solo := f.create(t, f.alex, "Alex's flat").ID
	custom := f.customItem(t, solo, "Kombucha")
	entry := newID()
	created := newID()
	resp, b := f.post(t, f.alex, solo, map[string]any{"id": entry, "eventId": created, "catalogItemId": custom})
	wantStatus(t, resp, b, http.StatusCreated, nil)
	resp, b = f.req(t, http.MethodPost, "/households/"+solo+"/invites", f.alex, "")
	wantStatus(t, resp, b, http.StatusCreated, nil)
	f.exec(t, `INSERT INTO household_item_defaults (household_id, catalog_item_id, location_id) VALUES ($1, $2, 2)`, solo, f.salt)
	// An entry in Home, which stays.
	homeEntry, _ := f.createEntry(t, f.milk, nil)

	resp, b = f.deleteMe(t, f.alex)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	f.wantAccountGone(t, f.alex, f.alexID, "alex")

	for _, table := range []string{"households WHERE id", "household_members WHERE household_id",
		"household_invites WHERE household_id", "catalog_items WHERE household_id",
		"household_item_defaults WHERE household_id", "pantry_entries WHERE household_id",
		"item_events WHERE household_id"} {
		if n := f.count(t, `SELECT count(*) FROM `+table+` = $1`, solo); n != 0 {
			t.Errorf("%s = Alex's flat: %d rows left, want 0", table, n)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM households WHERE id = $1`, f.home); n != 1 {
		t.Errorf("Home: %d rows, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1`, homeEntry.ID); n != 1 {
		t.Errorf("Sam's entry in Home: %d rows, want 1", n)
	}
	if got := f.list(t, f.sam); len(got) != 1 || got[0].ID != f.home {
		t.Errorf("Sam's households = %+v, want only Home", got)
	}
}

// TestDeleteMeOwnerOfSharedHousehold: an owner whose households have other
// members gets 409 naming them, and nothing is deleted.
func TestDeleteMeOwnerOfSharedHousehold(t *testing.T) {
	f := newDeleteFixture(t)
	f.registerToken(t, f.sam, "ExponentPushToken[sam-phone]")
	// Sam also owns "beach house" (shared with Alex) and "Attic" (alone).
	beach := f.create(t, f.sam, "beach house").ID
	f.exec(t, `INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'member')`, beach, f.alexID)
	attic := f.create(t, f.sam, "Attic").ID
	entry, _ := f.createEntry(t, f.milk, nil)

	snapshot := func() []int {
		return []int{
			f.count(t, `SELECT count(*) FROM users`),
			f.count(t, `SELECT count(*) FROM households`),
			f.count(t, `SELECT count(*) FROM household_members`),
			f.count(t, `SELECT count(*) FROM push_tokens`),
			f.count(t, `SELECT count(*) FROM pantry_entries WHERE created_by = $1`, f.samID),
			f.count(t, `SELECT count(*) FROM item_events WHERE actor_id = $1`, f.samID),
			f.count(t, `SELECT count(*) FROM deleted_accounts`),
		}
	}
	before := snapshot()

	resp, b := f.deleteMe(t, f.sam)
	var conflict openapi.DeleteAccountConflict
	wantStatus(t, resp, b, http.StatusConflict, &conflict)
	if conflict.Error.Code != CodeMustTransferOwnership {
		t.Errorf("code = %q, want %s", conflict.Error.Code, CodeMustTransferOwnership)
	}
	var got []string
	for _, h := range conflict.Error.Households {
		got = append(got, h.Id.String()+" "+h.Name)
	}
	// Sorted by name, ignoring case; Attic (Sam alone) is not listed.
	if want := []string{beach + " beach house", f.home + " Home"}; !slices.Equal(got, want) {
		t.Errorf("households = %v, want %v", got, want)
	}
	if msg := conflict.Error.Message; !strings.Contains(msg, `"beach house"`) || !strings.Contains(msg, `"Home"`) || strings.Contains(msg, "Attic") {
		t.Errorf("message = %q, want it to name beach house and Home only", msg)
	}

	if after := snapshot(); !slices.Equal(before, after) {
		t.Errorf("row counts changed from %v to %v; want nothing deleted", before, after)
	}
	if n := f.count(t, `SELECT count(*) FROM households WHERE id = $1`, attic); n != 1 {
		t.Errorf("Attic was deleted")
	}
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1 AND created_by = $2`, entry.ID, f.samID); n != 1 {
		t.Errorf("Sam's entry changed")
	}
	if calls := f.deleter.calls(); len(calls) != 0 {
		t.Errorf("Firebase deletions = %v, want none", calls)
	}
	// Sam can still use the API.
	if u := f.user(t, http.MethodGet, f.sam, ""); u["id"] != f.samID {
		t.Errorf("GET /me after 409 = %v", u)
	}

	// One household named: singular message.
	resp, b = f.req(t, http.MethodDelete, "/households/"+beach, f.sam, "")
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	resp, b = f.deleteMe(t, f.sam)
	wantStatus(t, resp, b, http.StatusConflict, &conflict)
	if len(conflict.Error.Households) != 1 || conflict.Error.Message != `Transfer ownership of "Home" to another member before deleting your account.` {
		t.Errorf("after deleting beach house: %s", b)
	}

	// After handing Home over, Sam can delete the account; Home stays with
	// Alex as its owner, and Attic (Sam alone) goes.
	resp, b = f.req(t, http.MethodPost, "/households/"+f.home+"/transfer-ownership", f.sam, `{"userId":"`+f.alexID+`"}`)
	wantStatus(t, resp, b, http.StatusOK, nil)
	resp, b = f.deleteMe(t, f.sam)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	f.wantAccountGone(t, f.sam, f.samID, "sam")
	if n := f.count(t, `SELECT count(*) FROM households WHERE id = $1`, attic); n != 0 {
		t.Errorf("Attic: %d rows, want 0", n)
	}
	if n := f.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1 AND user_id = $2 AND role = 'owner'`, f.home, f.alexID); n != 1 {
		t.Errorf("Home: Alex is not the owner")
	}
}

// TestDeleteMeCreatorInRemainingHousehold: a user who created a household,
// invites and custom items that outlive them can be deleted; the rows stay
// with a NULL creator.
func TestDeleteMeCreatorInRemainingHousehold(t *testing.T) {
	f := newDeleteFixture(t)
	// Sam created Home. Sam creates two invites and a custom item, then hands
	// Home to Alex.
	var invites []string
	for range 2 {
		var inv struct {
			ID string `json:"id"`
		}
		resp, b := f.req(t, http.MethodPost, "/households/"+f.home+"/invites", f.sam, "")
		wantStatus(t, resp, b, http.StatusCreated, &inv)
		invites = append(invites, inv.ID)
	}
	var item struct {
		ID string `json:"id"`
	}
	resp, b := f.req(t, http.MethodPost, "/households/"+f.home+"/catalog-items", f.sam,
		`{"name":"Chili crisp","categoryId":1,"locationId":1}`)
	wantStatus(t, resp, b, http.StatusCreated, &item)
	if n := f.count(t, `SELECT count(*) FROM catalog_items WHERE id = $1 AND created_by = $2`, item.ID, f.samID); n != 1 {
		t.Fatalf("custom item created_by is not Sam")
	}
	resp, b = f.req(t, http.MethodPost, "/households/"+f.home+"/transfer-ownership", f.sam, `{"userId":"`+f.alexID+`"}`)
	wantStatus(t, resp, b, http.StatusOK, nil)

	resp, b = f.deleteMe(t, f.sam)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	f.wantAccountGone(t, f.sam, f.samID, "sam")

	if n := f.count(t, `SELECT count(*) FROM households WHERE id = $1 AND created_by IS NULL`, f.home); n != 1 {
		t.Errorf("Home: want it kept with NULL created_by")
	}
	if n := f.count(t, `SELECT count(*) FROM household_invites WHERE id = ANY($1::uuid[]) AND created_by IS NULL`, invites); n != 2 {
		t.Errorf("invites kept with NULL created_by: %d, want 2", n)
	}
	if n := f.count(t, `SELECT count(*) FROM catalog_items WHERE id = $1 AND created_by IS NULL`, item.ID); n != 1 {
		t.Errorf("custom item: want it kept with NULL created_by")
	}
	// Alex, now the owner, still sees the invites.
	resp, b = f.req(t, http.MethodGet, "/households/"+f.home+"/invites", f.alex, "")
	wantStatus(t, resp, b, http.StatusOK, nil)
	for _, id := range invites {
		if !strings.Contains(string(b), id) {
			t.Errorf("Alex's invite list %s lacks %s", b, id)
		}
	}
}

// TestDeleteMeFirebaseFailureRollsBack: when the Firebase user cannot be
// deleted, nothing is deleted, the response is 503, and a retry works.
func TestDeleteMeFirebaseFailureRollsBack(t *testing.T) {
	f := newDeleteFixture(t)
	solo := f.create(t, f.alex, "Alex's flat").ID
	f.registerToken(t, f.alex, "ExponentPushToken[alex-phone]")
	entry, _ := f.createEntry(t, f.milk, nil)
	f.update(t, f.alex, entry.ID, map[string]any{"eventId": newID(), "level": "half"})

	f.deleter.setErr(errors.New("firebase: 503 backend unavailable"))
	resp, b := f.deleteMe(t, f.alex)
	wantError(t, resp, b, http.StatusServiceUnavailable, CodeAccountDeletionFailed)

	if n := f.count(t, `SELECT count(*) FROM users WHERE id = $1`, f.alexID); n != 1 {
		t.Errorf("users row: %d, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM households WHERE id = $1`, solo); n != 1 {
		t.Errorf("Alex's flat: %d rows, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM household_members WHERE user_id = $1`, f.alexID); n != 2 {
		t.Errorf("Alex's memberships: %d, want 2", n)
	}
	if n := f.count(t, `SELECT count(*) FROM push_tokens WHERE user_id = $1`, f.alexID); n != 1 {
		t.Errorf("Alex's push tokens: %d, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM pantry_entries WHERE id = $1 AND updated_by = $2`, entry.ID, f.alexID); n != 1 {
		t.Errorf("entry updated_by: want Alex")
	}
	if n := f.count(t, `SELECT count(*) FROM deleted_accounts`); n != 0 {
		t.Errorf("deleted_accounts: %d rows, want 0", n)
	}
	if u := f.user(t, http.MethodGet, f.alex, ""); u["id"] != f.alexID {
		t.Errorf("GET /me after the failure = %v, want Alex", u)
	}

	f.deleter.setErr(nil)
	resp, b = f.deleteMe(t, f.alex)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	f.wantAccountGone(t, f.alex, f.alexID, "alex")
}

// TestDeleteMeNotConfigured: without an account deleter nothing is deleted.
func TestDeleteMeNotConfigured(t *testing.T) {
	s := newMeServer(t)
	tok := s.verifier.Issue(auth.Token{UID: "u", Name: "U"})
	id := s.userID(t, tok)
	resp, b := s.req(t, http.MethodDelete, "/me", tok, "")
	wantError(t, resp, b, http.StatusServiceUnavailable, CodeAccountDeletionFailed)
	if n := s.count(t, `SELECT count(*) FROM users WHERE id = $1`, id); n != 1 {
		t.Errorf("users row: %d, want 1", n)
	}
}

func TestDeleteMeRequiresAuth(t *testing.T) {
	f := newDeleteFixture(t)
	resp, b := f.req(t, http.MethodDelete, "/me", "", "")
	wantError(t, resp, b, http.StatusUnauthorized, CodeUnauthorized)
	resp, b = f.req(t, http.MethodDelete, "/me", "not-a-token", "")
	wantError(t, resp, b, http.StatusUnauthorized, CodeUnauthorized)
	if n := f.count(t, `SELECT count(*) FROM users`); n != 2 {
		t.Errorf("users: %d, want 2", n)
	}
}

// TestDeletedAccountsArePruned: deleted_accounts keeps a UID for a day only;
// after that, the UID may sign up again (by then every old token has
// expired).
func TestDeletedAccountsArePruned(t *testing.T) {
	f := newDeleteFixture(t)
	f.exec(t, `INSERT INTO deleted_accounts (firebase_uid, deleted_at) VALUES
		('old', now() - interval '25 hours'), ('recent', now() - interval '23 hours')`)

	// "recent" was deleted less than a day ago: its token is refused.
	recent := f.verifier.Issue(auth.Token{UID: "recent", Name: "R"})
	resp, b := f.req(t, http.MethodGet, "/me", recent, "")
	wantError(t, resp, b, http.StatusUnauthorized, CodeUnauthorized)

	resp, b = f.deleteMe(t, f.alex)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	var uids []string
	rows, err := f.pool.Query(context.Background(), `SELECT firebase_uid FROM deleted_accounts ORDER BY firebase_uid`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			t.Fatal(err)
		}
		uids = append(uids, u)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if want := []string{"alex", "recent"}; !slices.Equal(uids, want) {
		t.Errorf("deleted_accounts = %v, want %v", uids, want)
	}

	// Once pruned, the UID is a new user again.
	f.exec(t, `DELETE FROM deleted_accounts WHERE firebase_uid = 'recent'`)
	if u := f.user(t, http.MethodGet, recent, ""); u["displayName"] != "R" {
		t.Errorf("GET /me for a pruned UID = %v", u)
	}
}

// TestConcurrentDeleteAndJoin: a join of the sole owner's household that runs
// while the account is deleted either lands first (409, nothing deleted) or
// finds the household gone; the household is never left without its owner.
func TestConcurrentDeleteAndJoin(t *testing.T) {
	for i := range 5 {
		f := newDeleteFixture(t)
		bobTok, bobID := f.newUser(t, "bob", "Bob")
		solo := f.create(t, f.alex, "Alex's flat").ID
		var inv struct {
			Code string `json:"code"`
		}
		resp, b := f.req(t, http.MethodPost, "/households/"+solo+"/invites", f.alex, "")
		wantStatus(t, resp, b, http.StatusCreated, &inv)

		var wg sync.WaitGroup
		var delStatus, joinStatus int
		wg.Add(2)
		go func() {
			defer wg.Done()
			resp, _ := f.deleteMe(t, f.alex)
			delStatus = resp.StatusCode
		}()
		go func() {
			defer wg.Done()
			resp, _ := f.req(t, http.MethodPost, "/invites/join", bobTok, `{"code":"`+inv.Code+`"}`)
			joinStatus = resp.StatusCode
		}()
		wg.Wait()

		households := f.count(t, `SELECT count(*) FROM households WHERE id = $1`, solo)
		owners := f.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1 AND role = 'owner'`, solo)
		bobIn := f.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1 AND user_id = $2`, solo, bobID)
		switch delStatus {
		case http.StatusNoContent:
			if households != 0 || joinStatus == http.StatusOK && bobIn != 0 {
				t.Errorf("run %d: deleted, but households=%d bobIn=%d join=%d", i, households, bobIn, joinStatus)
			}
		case http.StatusConflict:
			if households != 1 || owners != 1 || bobIn != 1 {
				t.Errorf("run %d: 409, but households=%d owners=%d bobIn=%d", i, households, owners, bobIn)
			}
		default:
			t.Errorf("run %d: DELETE /me = %d, join = %d", i, delStatus, joinStatus)
		}
	}
}
