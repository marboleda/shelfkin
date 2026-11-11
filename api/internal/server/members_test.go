package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// fakeAPI's members endpoints, so it keeps satisfying StrictServerInterface.

func (fakeAPI) ListMembers(context.Context, openapi.ListMembersRequestObject) (openapi.ListMembersResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) RemoveMember(context.Context, openapi.RemoveMemberRequestObject) (openapi.RemoveMemberResponseObject, error) {
	return nil, errNotImplemented
}

func (fakeAPI) TransferOwnership(context.Context, openapi.TransferOwnershipRequestObject) (openapi.TransferOwnershipResponseObject, error) {
	return nil, errNotImplemented
}

// member is a decoded Member response.
type member struct {
	UserID      string    `json:"userId"`
	DisplayName string    `json:"displayName"`
	Email       *string   `json:"email"`
	Role        string    `json:"role"`
	JoinedAt    time.Time `json:"joinedAt"`
}

// memberUser is one user of a membersFixture.
type memberUser struct {
	token string
	id    string
}

// membersFixture is a household owned by owner with members m1 and m2 (who
// joined in that order), and an outsider who is in no household.
type membersFixture struct {
	*meServer
	home                    household
	owner, m1, m2, outsider memberUser
}

func newMembersFixture(t *testing.T) *membersFixture {
	t.Helper()
	s := newMeServer(t)
	f := &membersFixture{meServer: s}
	newUser := func(uid, name, email string) memberUser {
		tok := s.verifier.Issue(auth.Token{UID: uid, Name: name, Email: email})
		return memberUser{token: tok, id: s.userID(t, tok)}
	}
	f.owner = newUser("alice", "Alice", "alice@example.com")
	f.m1 = newUser("bob", "Bob", "bob@example.com")
	f.m2 = newUser("carol", "Carol", "") // no email
	f.outsider = newUser("dave", "Dave", "dave@example.com")
	f.home = s.create(t, f.owner.token, "Home")
	// m2 joins after m1. Explicit times keep the order independent of clock
	// resolution.
	f.addMember(t, f.home.ID, f.m1.id, "1 hour")
	f.addMember(t, f.home.ID, f.m2.id, "2 hours")
	return f
}

// addMember adds userID to household as a member, joining the given interval
// after now.
func (f *membersFixture) addMember(t *testing.T, householdID, userID, after string) {
	t.Helper()
	f.exec(t, `INSERT INTO household_members (household_id, user_id, role, joined_at)
		VALUES ($1, $2, 'member', now() + $3::interval)`, householdID, userID, after)
}

func (f *membersFixture) members(t *testing.T, token, householdID string) []member {
	t.Helper()
	resp, b := f.req(t, http.MethodGet, "/households/"+householdID+"/members", token, "")
	var l struct {
		Members *[]member `json:"members"`
	}
	wantStatus(t, resp, b, http.StatusOK, &l)
	if l.Members == nil {
		t.Fatalf("GET members = %s, want a members array", b)
	}
	return *l.Members
}

func (f *membersFixture) remove(t *testing.T, token, householdID, userID string) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodDelete, "/households/"+householdID+"/members/"+userID, token, "")
}

func (f *membersFixture) transfer(t *testing.T, token, householdID, userID string) (*http.Response, []byte) {
	t.Helper()
	return f.req(t, http.MethodPost, "/households/"+householdID+"/transfer-ownership", token, `{"userId":"`+userID+`"}`)
}

func (f *membersFixture) owners(t *testing.T, householdID string) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(),
		`SELECT user_id::text FROM household_members WHERE household_id = $1 AND role = 'owner'`, householdID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func (f *membersFixture) isMember(t *testing.T, householdID, userID string) bool {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1 AND user_id = $2`, householdID, userID) == 1
}

// wantGone checks u gets 404 for the household and no longer sees it in
// GET /households.
func (f *membersFixture) wantGone(t *testing.T, u memberUser, householdID string) {
	t.Helper()
	resp, b := f.req(t, http.MethodGet, "/households/"+householdID, u.token, "")
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	resp, b = f.req(t, http.MethodGet, "/households/"+householdID+"/members", u.token, "")
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	for _, h := range f.list(t, u.token) {
		if h.ID == householdID {
			t.Errorf("GET /households still lists %s", householdID)
		}
	}
}

func roleOrder(ms []member) string {
	var parts []string
	for _, m := range ms {
		parts = append(parts, m.DisplayName+":"+m.Role)
	}
	return strings.Join(parts, ",")
}

func TestListMembers(t *testing.T) {
	f := newMembersFixture(t)

	// Every member sees the same list: the owner first, then by joinedAt.
	for _, u := range []memberUser{f.owner, f.m1, f.m2} {
		ms := f.members(t, u.token, f.home.ID)
		if got, want := roleOrder(ms), "Alice:owner,Bob:member,Carol:member"; got != want {
			t.Fatalf("members = %s, want %s", got, want)
		}
		if ms[0].UserID != f.owner.id || ms[1].UserID != f.m1.id || ms[2].UserID != f.m2.id {
			t.Errorf("userIds = %s, %s, %s; want %s, %s, %s", ms[0].UserID, ms[1].UserID, ms[2].UserID, f.owner.id, f.m1.id, f.m2.id)
		}
		if ms[0].Email == nil || *ms[0].Email != "alice@example.com" {
			t.Errorf("owner's email = %v, want alice@example.com", ms[0].Email)
		}
		if ms[2].Email != nil {
			t.Errorf("Carol's email = %q, want null", *ms[2].Email)
		}
		if !ms[1].JoinedAt.Before(ms[2].JoinedAt) || ms[0].JoinedAt.IsZero() {
			t.Errorf("joinedAt = %v, %v, %v; want set and increasing for the members", ms[0].JoinedAt, ms[1].JoinedAt, ms[2].JoinedAt)
		}
	}

	// The owner comes first even when they joined after a member.
	f.exec(t, `UPDATE household_members SET joined_at = now() + interval '1 day' WHERE household_id = $1 AND user_id = $2`, f.home.ID, f.owner.id)
	if got, want := roleOrder(f.members(t, f.m1.token, f.home.ID)), "Alice:owner,Bob:member,Carol:member"; got != want {
		t.Errorf("members with a late-joining owner = %s, want %s", got, want)
	}

	// Each member has exactly the five fields, and email is present as null.
	resp, b := f.req(t, http.MethodGet, "/households/"+f.home.ID+"/members", f.owner.token, "")
	var raw struct {
		Members []map[string]any `json:"members"`
	}
	wantStatus(t, resp, b, http.StatusOK, &raw)
	for _, m := range raw.Members {
		if len(m) != 5 {
			t.Errorf("member %v, want exactly userId, displayName, email, role, joinedAt", m)
		}
		if _, ok := m["email"]; !ok {
			t.Errorf("member %v has no email key", m)
		}
	}

	// Only this household's members.
	other := f.create(t, f.outsider.token, "Other")
	if got := roleOrder(f.members(t, f.outsider.token, other.ID)); got != "Dave:owner" {
		t.Errorf("Other's members = %s, want Dave:owner", got)
	}
}

func TestMembersHiddenFromNonMembers(t *testing.T) {
	f := newMembersFixture(t)
	missing := "0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"

	// The outsider's view of Home is the same as of a household that does
	// not exist, for every endpoint.
	for _, id := range []string{f.home.ID, missing} {
		resp, b := f.req(t, http.MethodGet, "/households/"+id+"/members", f.outsider.token, "")
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
		resp, b = f.remove(t, f.outsider.token, id, f.m1.id)
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
		resp, b = f.remove(t, f.outsider.token, id, f.outsider.id)
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
		resp, b = f.transfer(t, f.outsider.token, id, f.m1.id)
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
		resp, b = f.transfer(t, f.outsider.token, id, f.outsider.id)
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	}
	// Members of other households get 404 for a household that does not exist.
	resp, b := f.req(t, http.MethodGet, "/households/"+missing+"/members", f.owner.token, "")
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	resp, b = f.transfer(t, f.owner.token, missing, f.m1.id)
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	resp, b = f.remove(t, f.owner.token, missing, f.m1.id)
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)

	if got := roleOrder(f.members(t, f.owner.token, f.home.ID)); got != "Alice:owner,Bob:member,Carol:member" {
		t.Errorf("members after the outsider's attempts = %s", got)
	}
}

func TestOwnerRemovesMember(t *testing.T) {
	f := newMembersFixture(t)

	resp, b := f.remove(t, f.owner.token, f.home.ID, f.m1.id)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	if len(b) != 0 {
		t.Errorf("DELETE body = %q, want empty", b)
	}
	if f.isMember(t, f.home.ID, f.m1.id) {
		t.Error("Bob is still a member")
	}
	if got := roleOrder(f.members(t, f.owner.token, f.home.ID)); got != "Alice:owner,Carol:member" {
		t.Errorf("members = %s, want Alice:owner,Carol:member", got)
	}
	f.wantGone(t, f.m1, f.home.ID)

	// Removing them again: they are no longer a member.
	resp, b = f.remove(t, f.owner.token, f.home.ID, f.m1.id)
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
}

func TestMemberLeaves(t *testing.T) {
	f := newMembersFixture(t)

	resp, b := f.remove(t, f.m2.token, f.home.ID, f.m2.id)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	if f.isMember(t, f.home.ID, f.m2.id) {
		t.Error("Carol is still a member after leaving")
	}
	f.wantGone(t, f.m2, f.home.ID)
	if got := roleOrder(f.members(t, f.m1.token, f.home.ID)); got != "Alice:owner,Bob:member" {
		t.Errorf("members = %s, want Alice:owner,Bob:member", got)
	}
	// Leaving again: no longer a member of the household at all.
	resp, b = f.remove(t, f.m2.token, f.home.ID, f.m2.id)
	wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
}

func TestOwnerCannotLeave(t *testing.T) {
	f := newMembersFixture(t)

	resp, b := f.remove(t, f.owner.token, f.home.ID, f.owner.id)
	wantError(t, resp, b, http.StatusConflict, CodeOwnerCannotLeave)
	msg := strings.ToLower(string(b))
	if !strings.Contains(msg, "transfer ownership") || !strings.Contains(msg, "delete the household") {
		t.Errorf("message %s, want it to mention transferring ownership and deleting the household", b)
	}
	if got := f.owners(t, f.home.ID); len(got) != 1 || got[0] != f.owner.id {
		t.Errorf("owners = %v, want [%s]", got, f.owner.id)
	}

	// The same for an owner who is the only member.
	solo := f.create(t, f.outsider.token, "Solo")
	resp, b = f.remove(t, f.outsider.token, solo.ID, f.outsider.id)
	wantError(t, resp, b, http.StatusConflict, CodeOwnerCannotLeave)
	if !f.isMember(t, solo.ID, f.outsider.id) {
		t.Error("the sole owner was removed")
	}
}

func TestMemberCannotRemoveOthers(t *testing.T) {
	f := newMembersFixture(t)

	resp, b := f.remove(t, f.m1.token, f.home.ID, f.m2.id)
	wantError(t, resp, b, http.StatusForbidden, CodeOwnerOnly)
	resp, b = f.remove(t, f.m1.token, f.home.ID, f.owner.id)
	wantError(t, resp, b, http.StatusForbidden, CodeOwnerOnly)
	// Someone else who is not a member: still owner_only, the member cannot
	// remove anyone but themselves.
	resp, b = f.remove(t, f.m1.token, f.home.ID, f.outsider.id)
	wantError(t, resp, b, http.StatusForbidden, CodeOwnerOnly)

	if n := f.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1`, f.home.ID); n != 3 {
		t.Errorf("%d members, want 3", n)
	}
}

func TestRemoveNonMember(t *testing.T) {
	f := newMembersFixture(t)
	other := f.create(t, f.outsider.token, "Other")

	// A user in another household, and an ID that is no user at all.
	for _, id := range []string{f.outsider.id, "0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"} {
		resp, b := f.remove(t, f.owner.token, f.home.ID, id)
		wantError(t, resp, b, http.StatusNotFound, CodeNotFound)
	}
	if !f.isMember(t, other.ID, f.outsider.id) {
		t.Error("the outsider lost their own household")
	}
	if n := f.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1`, f.home.ID); n != 3 {
		t.Errorf("%d members, want 3", n)
	}
}

func TestTransferOwnershipThenLeave(t *testing.T) {
	f := newMembersFixture(t)

	resp, b := f.transfer(t, f.owner.token, f.home.ID, f.m1.id)
	var l struct {
		Members []member `json:"members"`
	}
	wantStatus(t, resp, b, http.StatusOK, &l)
	if got, want := roleOrder(l.Members), "Bob:owner,Alice:member,Carol:member"; got != want {
		t.Errorf("transfer response = %s, want %s", got, want)
	}
	if got := f.owners(t, f.home.ID); len(got) != 1 || got[0] != f.m1.id {
		t.Errorf("owners after transfer = %v, want only Bob (%s)", got, f.m1.id)
	}
	if got := roleOrder(f.members(t, f.m2.token, f.home.ID)); got != "Bob:owner,Alice:member,Carol:member" {
		t.Errorf("members after transfer = %s", got)
	}

	// The roles show in GET /households/{id}.
	var h household
	resp, b = f.req(t, http.MethodGet, "/households/"+f.home.ID, f.owner.token, "")
	wantStatus(t, resp, b, http.StatusOK, &h)
	if h.Role != "member" {
		t.Errorf("old owner's role = %q, want member", h.Role)
	}
	resp, b = f.req(t, http.MethodGet, "/households/"+f.home.ID, f.m1.token, "")
	wantStatus(t, resp, b, http.StatusOK, &h)
	if h.Role != "owner" {
		t.Errorf("new owner's role = %q, want owner", h.Role)
	}

	// The old owner has lost the owner's rights...
	resp, b = f.transfer(t, f.owner.token, f.home.ID, f.m2.id)
	wantError(t, resp, b, http.StatusForbidden, CodeOwnerOnly)
	resp, b = f.remove(t, f.owner.token, f.home.ID, f.m2.id)
	wantError(t, resp, b, http.StatusForbidden, CodeOwnerOnly)
	// ...the new owner cannot leave...
	resp, b = f.remove(t, f.m1.token, f.home.ID, f.m1.id)
	wantError(t, resp, b, http.StatusConflict, CodeOwnerCannotLeave)
	// ...and the old owner can now leave.
	resp, b = f.remove(t, f.owner.token, f.home.ID, f.owner.id)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	f.wantGone(t, f.owner, f.home.ID)
	if got := roleOrder(f.members(t, f.m1.token, f.home.ID)); got != "Bob:owner,Carol:member" {
		t.Errorf("members after the old owner left = %s", got)
	}
}

func TestTransferOwnershipErrors(t *testing.T) {
	f := newMembersFixture(t)
	path := "/households/" + f.home.ID + "/transfer-ownership"

	// Only the owner can transfer.
	resp, b := f.transfer(t, f.m1.token, f.home.ID, f.m2.id)
	wantError(t, resp, b, http.StatusForbidden, CodeOwnerOnly)
	resp, b = f.transfer(t, f.m1.token, f.home.ID, f.m1.id)
	wantError(t, resp, b, http.StatusForbidden, CodeOwnerOnly)

	// To someone who is not a member: a user in no household, a user who
	// owns another household, and an ID that is no user.
	f.create(t, f.outsider.token, "Other")
	for _, id := range []string{f.outsider.id, "0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"} {
		resp, b = f.transfer(t, f.owner.token, f.home.ID, id)
		wantError(t, resp, b, http.StatusBadRequest, CodeNotAMember)
	}
	// To a member who has left.
	resp, b = f.remove(t, f.m2.token, f.home.ID, f.m2.id)
	wantStatus(t, resp, b, http.StatusNoContent, nil)
	resp, b = f.transfer(t, f.owner.token, f.home.ID, f.m2.id)
	wantError(t, resp, b, http.StatusBadRequest, CodeNotAMember)

	// To yourself.
	resp, b = f.transfer(t, f.owner.token, f.home.ID, f.owner.id)
	wantError(t, resp, b, http.StatusBadRequest, CodeAlreadyOwner)

	// Malformed requests.
	for what, body := range map[string]string{
		"not JSON":          `{"userId":`,
		"userId not uuid":   `{"userId":"bob"}`,
		"userId not string": `{"userId":1}`,
	} {
		resp, b = f.req(t, http.MethodPost, path, f.owner.token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body %s)", what, resp.StatusCode, b)
		}
	}
	resp, b = f.req(t, http.MethodPost, path, f.owner.token, `{}`)
	wantError(t, resp, b, http.StatusBadRequest, CodeNotAMember)

	if got := f.owners(t, f.home.ID); len(got) != 1 || got[0] != f.owner.id {
		t.Errorf("owners after rejected transfers = %v, want only Alice", got)
	}
}

// TestConcurrentTransfers sends two transfers of the same household at once,
// to different members. Exactly one wins; the other gets 409 conflict or 403
// owner_only (if it ran after the first committed), never 500.
func TestConcurrentTransfers(t *testing.T) {
	f := newMembersFixture(t)

	for round := range 10 {
		h := f.create(t, f.owner.token, "Race")
		f.addMember(t, h.ID, f.m1.id, "1 hour")
		f.addMember(t, h.ID, f.m2.id, "2 hours")

		targets := []string{f.m1.id, f.m2.id}
		statuses := make([]int, len(targets))
		bodies := make([][]byte, len(targets))
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i, target := range targets {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				resp, b := f.transfer(t, f.owner.token, h.ID, target)
				statuses[i], bodies[i] = resp.StatusCode, b
			}()
		}
		close(start)
		wg.Wait()

		wins := 0
		winner := ""
		for i, status := range statuses {
			switch status {
			case http.StatusOK:
				wins++
				winner = targets[i]
			case http.StatusConflict, http.StatusForbidden:
				body := string(bodies[i])
				if !strings.Contains(body, `"`+CodeConflict+`"`) && !strings.Contains(body, `"`+CodeOwnerOnly+`"`) {
					t.Errorf("round %d: losing body %s, want code conflict or owner_only", round, body)
				}
			default:
				t.Errorf("round %d: status %d (body %s), want 200, 409 or 403", round, status, bodies[i])
			}
		}
		if wins != 1 {
			t.Fatalf("round %d: %d transfers succeeded (%v), want exactly 1", round, wins, statuses)
		}
		if got := f.owners(t, h.ID); len(got) != 1 || got[0] != winner {
			t.Fatalf("round %d: owners = %v, want only the winner %s", round, got, winner)
		}
		if n := f.count(t, `SELECT count(*) FROM household_members WHERE household_id = $1`, h.ID); n != 3 {
			t.Fatalf("round %d: %d members, want 3", round, n)
		}
	}
}

// TestConcurrentTransferAndRemove races the owner transferring to m1 against
// the owner removing m1. Whatever the order, the household ends with exactly
// one owner and no request fails with 500.
func TestConcurrentTransferAndRemove(t *testing.T) {
	f := newMembersFixture(t)

	for round := range 10 {
		h := f.create(t, f.owner.token, "Race")
		f.addMember(t, h.ID, f.m1.id, "1 hour")

		var transferStatus, removeStatus int
		var transferBody, removeBody []byte
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			resp, b := f.transfer(t, f.owner.token, h.ID, f.m1.id)
			transferStatus, transferBody = resp.StatusCode, b
		}()
		go func() {
			defer wg.Done()
			<-start
			resp, b := f.remove(t, f.owner.token, h.ID, f.m1.id)
			removeStatus, removeBody = resp.StatusCode, b
		}()
		close(start)
		wg.Wait()

		if transferStatus >= 500 || removeStatus >= 500 {
			t.Fatalf("round %d: transfer %d (%s), remove %d (%s); want no 5xx",
				round, transferStatus, transferBody, removeStatus, removeBody)
		}
		if transferStatus == http.StatusOK && removeStatus == http.StatusNoContent {
			t.Fatalf("round %d: both the transfer and the removal succeeded", round)
		}
		if got := f.owners(t, h.ID); len(got) != 1 {
			t.Fatalf("round %d: owners = %v, want exactly one (transfer %d, remove %d)", round, got, transferStatus, removeStatus)
		}
	}
}

// TestRemovedMembersHistoryStays checks a removed member's entries and events
// stay, still pointing at their user (whose name the "last changed by"
// labels show).
func TestRemovedMembersHistoryStays(t *testing.T) {
	f := newMembersFixture(t)
	f.exec(t, `
		WITH e AS (
		    INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level, created_by, updated_by)
		    VALUES (uuidv7(), $1,
		            (SELECT id FROM catalog_items WHERE household_id IS NULL LIMIT 1),
		            (SELECT min(id) FROM locations), 'full', $2, $2)
		    RETURNING id, catalog_item_id
		)
		INSERT INTO item_events (id, household_id, entry_id, catalog_item_id, type, snapshot, actor_id)
		SELECT uuidv7(), $1, e.id, e.catalog_item_id, 'entry_created', '{}', $2 FROM e`, f.home.ID, f.m1.id)

	resp, b := f.remove(t, f.owner.token, f.home.ID, f.m1.id)
	wantStatus(t, resp, b, http.StatusNoContent, nil)

	if n := f.count(t, `
		SELECT count(*) FROM pantry_entries p JOIN users u ON u.id = p.updated_by
		WHERE p.household_id = $1 AND p.created_by = $2 AND u.display_name = 'Bob'`, f.home.ID, f.m1.id); n != 1 {
		t.Errorf("%d of Bob's entries left with his name, want 1", n)
	}
	if n := f.count(t, `
		SELECT count(*) FROM item_events e JOIN users u ON u.id = e.actor_id
		WHERE e.household_id = $1 AND u.display_name = 'Bob'`, f.home.ID); n != 1 {
		t.Errorf("%d of Bob's events left with his name, want 1", n)
	}
}

func TestMembersMalformedIDs(t *testing.T) {
	f := newMembersFixture(t)
	for _, r := range []struct{ method, path, body string }{
		{http.MethodGet, "/households/not-a-uuid/members", ""},
		{http.MethodDelete, "/households/not-a-uuid/members/" + f.m1.id, ""},
		{http.MethodDelete, "/households/" + f.home.ID + "/members/not-a-uuid", ""},
		{http.MethodPost, "/households/not-a-uuid/transfer-ownership", `{"userId":"` + f.m1.id + `"}`},
	} {
		resp, b := f.req(t, r.method, r.path, f.owner.token, r.body)
		wantError(t, resp, b, http.StatusBadRequest, CodeBadRequest)
	}
}

func TestMembersRequireAuth(t *testing.T) {
	s := newMeServer(t)
	const id = "0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80"
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/households/" + id + "/members"},
		{http.MethodDelete, "/households/" + id + "/members/" + id},
		{http.MethodPost, "/households/" + id + "/transfer-ownership"},
	} {
		resp, b := s.req(t, r.method, r.path, "not-issued", "")
		wantError(t, resp, b, http.StatusUnauthorized, CodeUnauthorized)
	}
}
