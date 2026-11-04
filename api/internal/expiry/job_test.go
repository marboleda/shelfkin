package expiry_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/expiry"
)

// fakeSender records every batch and answers with ticket(msg) for each
// message, or with err for the whole batch when failBatch(msgs) is true.
type fakeSender struct {
	mu        sync.Mutex
	batches   [][]expiry.Message
	ticket    func(expiry.Message) expiry.Ticket
	failBatch func([]expiry.Message) bool
}

func (f *fakeSender) Send(_ context.Context, msgs []expiry.Message) ([]expiry.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(msgs) > expiry.MaxBatchSize {
		return nil, fmt.Errorf("fake: batch of %d", len(msgs))
	}
	f.batches = append(f.batches, slices.Clone(msgs))
	if f.failBatch != nil && f.failBatch(msgs) {
		return nil, errors.New("fake: HTTP 400")
	}
	tickets := make([]expiry.Ticket, len(msgs))
	for i, m := range msgs {
		if f.ticket != nil {
			tickets[i] = f.ticket(m)
		} else {
			tickets[i] = expiry.Ticket{Status: expiry.TicketOK}
		}
	}
	return tickets, nil
}

// sent returns and forgets every message sent so far.
func (f *fakeSender) sent() []expiry.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []expiry.Message
	for _, b := range f.batches {
		all = append(all, b...)
	}
	f.batches = nil
	return all
}

func errorTicket(code string) expiry.Ticket {
	t := expiry.Ticket{Status: expiry.TicketError, Message: "rejected"}
	t.Details.Error = code
	return t
}

// fixture inserts test data as the pool's superuser, bypassing RLS.
type fixture struct {
	t      *testing.T
	pool   *pgxpool.Pool
	sender *fakeSender
	logs   *bytes.Buffer
	seq    int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{t: t, pool: dbtest.New(t), sender: &fakeSender{}, logs: &bytes.Buffer{}}
}

func (f *fixture) job(dryRun bool) *expiry.Job {
	return &expiry.Job{
		DB:     f.pool,
		Sender: f.sender,
		Logger: slog.New(slog.NewJSONHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		DryRun: dryRun,
	}
}

// run runs the job at now (RFC 3339) and returns its summary.
func (f *fixture) run(now string) expiry.Summary {
	f.t.Helper()
	sum, err := f.runErr(now, false)
	if err != nil {
		f.t.Fatalf("run at %s: %v", now, err)
	}
	return sum
}

func (f *fixture) runErr(now string, dryRun bool) (expiry.Summary, error) {
	f.t.Helper()
	at, err := time.Parse(time.RFC3339, now)
	if err != nil {
		f.t.Fatal(err)
	}
	return f.job(dryRun).Run(f.t.Context(), at)
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.t.Context(), sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *fixture) id(sql string, args ...any) string {
	f.t.Helper()
	var id string
	if err := f.pool.QueryRow(f.t.Context(), sql, args...).Scan(&id); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func (f *fixture) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.t.Context(), sql, args...).Scan(&n); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// user inserts a user with the given time zone and push setting.
func (f *fixture) user(timezone string, pushEnabled bool) string {
	f.t.Helper()
	f.seq++
	return f.id(`INSERT INTO users (firebase_uid, display_name, timezone, push_enabled)
		VALUES ($1, 'User', $2, $3) RETURNING id::text`, fmt.Sprintf("fb-%d", f.seq), timezone, pushEnabled)
}

// token adds a push token for the user and returns the token.
func (f *fixture) token(userID string) string {
	f.t.Helper()
	f.seq++
	tok := fmt.Sprintf("ExponentPushToken[%d]", f.seq)
	f.exec(`INSERT INTO push_tokens (user_id, token, platform) VALUES ($1, $2, 'ios')`, userID, tok)
	return tok
}

// pushUser is an opted-in user with one token.
func (f *fixture) pushUser(timezone string) (userID, token string) {
	userID = f.user(timezone, true)
	return userID, f.token(userID)
}

// household creates a household with the given members (the first is the
// owner).
func (f *fixture) household(name string, members ...string) string {
	f.t.Helper()
	h := f.id(`INSERT INTO households (name) VALUES ($1) RETURNING id::text`, name)
	for i, m := range members {
		role := "member"
		if i == 0 {
			role = "owner"
		}
		f.exec(`INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, $3)`, h, m, role)
	}
	return h
}

// entry adds an entry of a custom item called name. expiresOn is a date
// (YYYY-MM-DD) or "" for none.
func (f *fixture) entry(householdID, name, level, expiresOn string) string {
	f.t.Helper()
	item := f.id(`
		WITH ins AS (
		    INSERT INTO catalog_items (household_id, name, default_category_id, default_location_id)
		    VALUES ($1, $2, 1, 2)
		    ON CONFLICT DO NOTHING RETURNING id
		)
		SELECT id::text FROM ins
		UNION ALL
		SELECT id::text FROM catalog_items WHERE household_id = $1 AND lower(name) = lower($2)
		LIMIT 1`, householdID, name)
	var expires any
	if expiresOn != "" {
		expires = expiresOn
	}
	return f.id(`INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level, expires_on)
		VALUES (uuidv7(), $1, $2, 2, $3, $4::date) RETURNING id::text`, householdID, item, level, expires)
}

func (f *fixture) notifications(userID string) int {
	f.t.Helper()
	return f.count(`SELECT count(*) FROM expiry_notifications WHERE user_id = $1`, userID)
}

// recipients returns the tokens messages went to, sorted.
func recipients(msgs []expiry.Message) []string {
	var to []string
	for _, m := range msgs {
		to = append(to, m.To)
	}
	slices.Sort(to)
	return to
}

func sorted(s ...string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

// 2026-10-27 is a Tuesday; Toronto is on EDT (UTC-4) until 2026-11-01.
const (
	torontoNine = "2026-10-27T13:00:00Z"
	today       = "2026-10-27"
)

func TestSelectsUsersByLocalHour(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	toronto, torontoTok := f.pushUser("America/Toronto")
	utc, utcTok := f.pushUser("UTC")
	kolkata, kolkataTok := f.pushUser("Asia/Kolkata")
	for _, u := range []string{toronto, utc, kolkata} {
		f.entry(f.household("Home", u), "Milk", "full", "2026-10-28")
	}

	for _, tc := range []struct {
		now  string
		want []string
	}{
		{"2026-10-27T03:00:00Z", nil},                  // Kolkata 08:30, Toronto 23:00
		{"2026-10-27T04:00:00Z", []string{kolkataTok}}, // Kolkata 09:30
		{"2026-10-27T09:00:00Z", []string{utcTok}},
		{"2026-10-27T13:00:00Z", []string{torontoTok}}, // Toronto 09:00, UTC 13:00
	} {
		sum := f.run(tc.now)
		if got := recipients(f.sender.sent()); !slices.Equal(got, tc.want) {
			t.Errorf("at %s: sent to %v, want %v", tc.now, got, tc.want)
		}
		if sum.UsersProcessed != len(tc.want) || sum.PushesSent != len(tc.want) || sum.Failures != 0 {
			t.Errorf("at %s: summary %+v", tc.now, sum)
		}
	}
}

func TestInvalidTimezoneUsesUTC(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, tz := range []string{"Mars/Olympus_Mons", "", "Local"} {
		u, tok := f.pushUser(tz)
		f.entry(f.household("Home", u), "Milk", "full", today)

		f.run(torontoNine)
		if got := f.sender.sent(); len(got) != 0 {
			t.Errorf("timezone %q: sent %d pushes at 13:00 UTC", tz, len(got))
		}
		f.run("2026-10-27T09:00:00Z")
		if got := recipients(f.sender.sent()); !slices.Equal(got, []string{tok}) {
			t.Errorf("timezone %q: at 09:00 UTC sent to %v, want %v", tz, got, tok)
		}
		if !strings.Contains(f.logs.String(), `"level":"WARN","msg":"unknown time zone, using UTC","user_id":"`+u+`"`) {
			t.Errorf("timezone %q: no warning with the user ID in logs:\n%s", tz, f.logs)
		}
	}
}

func TestMessageContent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, tok := f.pushUser("America/Toronto")
	h := f.household("Cabin", u)
	// Inserted out of order; the body lists soonest first, including items
	// that have already expired.
	f.entry(h, "Spinach", "half", "2026-10-29")
	f.entry(h, "Yogurt", "low", "2026-10-30")
	f.entry(h, "Milk", "full", "2026-10-20")
	f.entry(h, "Eggs", "full", today)
	f.entry(h, "Bread", "full", "2026-10-30")

	sum := f.run(torontoNine)
	msgs := f.sender.sent()
	if len(msgs) != 1 {
		t.Fatalf("sent %d pushes, want 1", len(msgs))
	}
	m := msgs[0]
	want := expiry.Message{
		To:    tok,
		Title: "Expiring soon in Cabin",
		Body:  "Milk, Eggs, Spinach and 2 more",
		Data:  map[string]string{"householdId": h},
		Sound: "default",
	}
	if m.To != want.To || m.Title != want.Title || m.Body != want.Body || m.Data["householdId"] != h || len(m.Data) != 1 {
		t.Errorf("sent %+v, want %+v", m, want)
	}
	if n := f.notifications(u); n != 5 {
		t.Errorf("%d expiry_notifications rows, want 5", n)
	}
	if sum != (expiry.Summary{UsersProcessed: 1, PushesSent: 1}) {
		t.Errorf("summary %+v", sum)
	}
	if !strings.Contains(f.logs.String(), `"msg":"expiry job finished"`) ||
		!strings.Contains(f.logs.String(), `"users_processed":1,"pushes_sent":1,"failures":0`) {
		t.Errorf("no summary line in logs:\n%s", f.logs)
	}
}

func TestBody(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{[]string{"Milk"}, "Milk"},
		{[]string{"Milk", "Eggs"}, "Milk, Eggs"},
		{[]string{"Milk", "Eggs", "Spinach"}, "Milk, Eggs, Spinach"},
		{[]string{"Milk", "Eggs", "Spinach", "Bread"}, "Milk, Eggs, Spinach and 1 more"},
		{[]string{"Milk", "Eggs", "Spinach", "Bread", "Yogurt"}, "Milk, Eggs, Spinach and 2 more"},
		{[]string{"Milk", "Milk", "Eggs"}, "Milk, Eggs"},
	} {
		if got := expiry.Body(tc.names); got != tc.want {
			t.Errorf("Body(%q) = %q, want %q", tc.names, got, tc.want)
		}
	}
}

func TestBodyItemCounts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	names := []string{"Apples", "Butter", "Cheese", "Dates", "Eggs"}
	want := map[int]string{
		1: "Apples",
		3: "Apples, Butter, Cheese",
		5: "Apples, Butter, Cheese and 2 more",
	}
	for n, body := range want {
		u, _ := f.pushUser("America/Toronto")
		h := f.household("Home", u)
		for i := range n {
			f.entry(h, names[i], "full", fmt.Sprintf("2026-10-%02d", 20+i))
		}
		f.run(torontoNine)
		msgs := f.sender.sent()
		if len(msgs) != 1 || msgs[0].Body != body {
			t.Errorf("%d items: sent %+v, want body %q", n, msgs, body)
		}
		// Announced entries are not sent again on a later run in the same
		// hour, so each iteration only sees the new user.
	}
}

func TestDeduplication(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, _ := f.pushUser("America/Toronto")
	h := f.household("Home", u)
	f.entry(h, "Milk", "full", today)

	f.run(torontoNine)
	if got := f.sender.sent(); len(got) != 1 {
		t.Fatalf("first run sent %d pushes, want 1", len(got))
	}
	if sum := f.run(torontoNine); sum.PushesSent != 0 || len(f.sender.sent()) != 0 {
		t.Errorf("second run in the same hour sent pushes: %+v", sum)
	}

	// The next day only the new entry is announced.
	f.entry(h, "Eggs", "full", "2026-10-29")
	f.run("2026-10-28T13:00:00Z")
	msgs := f.sender.sent()
	if len(msgs) != 1 || msgs[0].Body != "Eggs" {
		t.Errorf("next day sent %+v, want one push with body Eggs", msgs)
	}

	// The day after, nothing new.
	f.run("2026-10-29T13:00:00Z")
	if got := f.sender.sent(); len(got) != 0 {
		t.Errorf("third day sent %+v", got)
	}
}

func TestEditedExpiryIsAnnouncedAgain(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, _ := f.pushUser("America/Toronto")
	entry := f.entry(f.household("Home", u), "Milk", "full", today)

	f.run(torontoNine)
	if got := f.sender.sent(); len(got) != 1 {
		t.Fatalf("first run sent %d pushes, want 1", len(got))
	}
	f.exec(`UPDATE pantry_entries SET expires_on = '2026-10-30' WHERE id = $1`, entry)
	f.run("2026-10-28T13:00:00Z")
	msgs := f.sender.sent()
	if len(msgs) != 1 || msgs[0].Body != "Milk" {
		t.Errorf("after editing the expiry sent %+v, want one push for Milk", msgs)
	}
	if n := f.notifications(u); n != 2 {
		t.Errorf("%d expiry_notifications rows, want 2 (one per expiry date)", n)
	}
}

func TestExclusions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, tok := f.pushUser("America/Toronto")
	optedOut := f.user("America/Toronto", false)
	f.token(optedOut)
	noTokens := f.user("America/Toronto", true)
	h := f.household("Home", u, optedOut, noTokens)

	f.entry(h, "Milk", "full", today)
	f.entry(h, "Gone", "out", today)
	f.entry(h, "Salt", "full", "")
	f.entry(h, "Later", "full", "2026-10-31") // today + 4
	f.entry(h, "Edge", "full", "2026-10-30")  // today + 3
	// A household the user is not in.
	f.entry(f.household("Elsewhere", f.user("UTC", false)), "Foreign", "full", today)

	sum := f.run(torontoNine)
	msgs := f.sender.sent()
	if len(msgs) != 1 || msgs[0].To != tok || msgs[0].Body != "Milk, Edge" {
		t.Errorf("sent %+v, want one push to %s with body \"Milk, Edge\"", msgs, tok)
	}
	if sum.UsersProcessed != 1 {
		t.Errorf("processed %d users, want 1", sum.UsersProcessed)
	}
	if n := f.notifications(u); n != 2 {
		t.Errorf("%d expiry_notifications rows, want 2", n)
	}
	if n := f.count(`SELECT count(*) FROM expiry_notifications WHERE user_id <> $1`, u); n != 0 {
		t.Errorf("%d rows for other users", n)
	}
}

func TestOnePushPerHouseholdPerUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sam, samTok := f.pushUser("America/Toronto")
	alex, alexTok := f.pushUser("America/Toronto")
	home := f.household("Home", sam, alex)
	cabin := f.household("Cabin", sam)
	empty := f.household("Empty", sam)
	f.entry(home, "Milk", "full", today)
	f.entry(cabin, "Eggs", "full", today)
	f.entry(empty, "Rice", "full", "2026-12-01")

	sum := f.run(torontoNine)
	got := map[string][]string{}
	for _, m := range f.sender.sent() {
		got[m.To] = append(got[m.To], m.Title+"|"+m.Data["householdId"])
	}
	wantSam := sorted("Expiring soon in Home|"+home, "Expiring soon in Cabin|"+cabin)
	if s := sorted(got[samTok]...); !slices.Equal(s, wantSam) {
		t.Errorf("Sam got %v, want %v", s, wantSam)
	}
	if a := got[alexTok]; !slices.Equal(a, []string{"Expiring soon in Home|" + home}) {
		t.Errorf("Alex got %v", a)
	}
	if sum != (expiry.Summary{UsersProcessed: 2, PushesSent: 3}) {
		t.Errorf("summary %+v", sum)
	}
}

func TestEveryTokenGetsThePush(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.user("America/Toronto", true)
	// More than one batch worth of tokens.
	var tokens []string
	for range expiry.MaxBatchSize + 5 {
		tokens = append(tokens, f.token(u))
	}
	f.entry(f.household("Home", u), "Milk", "full", today)

	sum := f.run(torontoNine)
	if len(f.sender.batches) != 2 || len(f.sender.batches[0]) != expiry.MaxBatchSize {
		t.Errorf("batch sizes %d", len(f.sender.batches))
	}
	if got := recipients(f.sender.sent()); !slices.Equal(got, sorted(tokens...)) {
		t.Errorf("sent to %d tokens, want %d", len(got), len(tokens))
	}
	if sum.PushesSent != len(tokens) {
		t.Errorf("pushes sent %d", sum.PushesSent)
	}
}

func TestDeviceNotRegisteredDeletesToken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, good := f.pushUser("America/Toronto")
	dead := f.token(u)
	f.entry(f.household("Home", u), "Milk", "full", today)
	f.sender.ticket = func(m expiry.Message) expiry.Ticket {
		if m.To == dead {
			return errorTicket(expiry.ErrorDeviceNotRegistered)
		}
		return expiry.Ticket{Status: expiry.TicketOK}
	}

	sum := f.run(torontoNine)
	if n := f.count(`SELECT count(*) FROM push_tokens WHERE token = $1`, dead); n != 0 {
		t.Error("unregistered token was not deleted")
	}
	if n := f.count(`SELECT count(*) FROM push_tokens WHERE token = $1`, good); n != 1 {
		t.Error("working token was deleted")
	}
	if n := f.notifications(u); n != 1 {
		t.Errorf("%d expiry_notifications rows, want 1 (one token accepted it)", n)
	}
	if sum.Failures != 0 || sum.PushesSent != 1 {
		t.Errorf("summary %+v", sum)
	}
	if strings.Contains(f.logs.String(), dead) || strings.Contains(f.logs.String(), good) {
		t.Errorf("logs contain a push token:\n%s", f.logs)
	}
}

func TestAllTokensFailingRecordsNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, tok1 := f.pushUser("America/Toronto")
	tok2 := f.token(u)
	f.entry(f.household("Home", u), "Milk", "full", today)
	other, otherTok := f.pushUser("America/Toronto")
	f.entry(f.household("Other", other), "Eggs", "full", today)
	f.sender.ticket = func(m expiry.Message) expiry.Ticket {
		switch m.To {
		case tok1:
			return errorTicket(expiry.ErrorDeviceNotRegistered)
		case tok2:
			return errorTicket("MessageRateExceeded")
		}
		return expiry.Ticket{Status: expiry.TicketOK}
	}

	sum, err := f.runErr(torontoNine, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := f.notifications(u); n != 0 {
		t.Errorf("%d expiry_notifications rows after every token failed, want 0", n)
	}
	if n := f.notifications(other); n != 1 {
		t.Errorf("the other user has %d rows, want 1", n)
	}
	if sum != (expiry.Summary{UsersProcessed: 2, PushesSent: 1, Failures: 1}) {
		t.Errorf("summary %+v", sum)
	}
	logs := f.logs.String()
	if !strings.Contains(logs, `"user_id":"`+u+`"`) || !strings.Contains(logs, "MessageRateExceeded") {
		t.Errorf("failure not logged with the user ID:\n%s", logs)
	}
	for _, secret := range []string{tok1, tok2, otherTok, "Milk", "Eggs"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain %q:\n%s", secret, logs)
		}
	}

	// The entries are tried again (the next day here, as the job only runs
	// for the user at 9am).
	f.sender.sent()
	f.sender.ticket = nil
	f.run("2026-10-28T13:00:00Z")
	if got := recipients(f.sender.sent()); !slices.Equal(got, []string{tok2}) {
		t.Errorf("retry sent to %v, want the remaining token", got)
	}
	if n := f.notifications(u); n != 1 {
		t.Errorf("%d rows after the retry, want 1", n)
	}
}

func TestSendErrorDoesNotStopOtherUsers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	bad, badTok := f.pushUser("America/Toronto")
	good, goodTok := f.pushUser("America/Toronto")
	f.entry(f.household("Home", bad), "Milk", "full", today)
	f.entry(f.household("Home", good), "Milk", "full", today)
	f.sender.failBatch = func(msgs []expiry.Message) bool { return msgs[0].To == badTok }

	sum, err := f.runErr(torontoNine, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := recipients(f.sender.sent()); !slices.Equal(got, sorted(badTok, goodTok)) {
		t.Errorf("sent to %v", got)
	}
	if f.notifications(bad) != 0 || f.notifications(good) != 1 {
		t.Errorf("rows: failed user %d, other user %d; want 0 and 1", f.notifications(bad), f.notifications(good))
	}
	if sum != (expiry.Summary{UsersProcessed: 2, PushesSent: 1, Failures: 1}) {
		t.Errorf("summary %+v", sum)
	}
	if !strings.Contains(f.logs.String(), `"msg":"expiry job finished"`) ||
		!strings.Contains(f.logs.String(), `"failures":1`) {
		t.Errorf("summary line missing failures:\n%s", f.logs)
	}
}

func TestDeletesOldNotifications(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.user("UTC", false)
	for _, d := range []string{"2026-09-26", "2026-09-27", "2026-10-20"} {
		f.exec(`INSERT INTO expiry_notifications (entry_id, user_id, expires_on) VALUES (uuidv7(), $1, $2)`, u, d)
	}

	// Dry run deletes nothing.
	if _, err := f.runErr(torontoNine, true); err != nil {
		t.Fatal(err)
	}
	if n := f.notifications(u); n != 3 {
		t.Errorf("dry run left %d rows, want 3", n)
	}

	// 2026-09-26 is 31 days before 2026-10-27; 2026-09-27 is exactly 30.
	f.run(torontoNine)
	var left []string
	rows, err := f.pool.Query(t.Context(), `SELECT expires_on::text FROM expiry_notifications ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		left = append(left, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"2026-09-27", "2026-10-20"}; !slices.Equal(left, want) {
		t.Errorf("rows left %v, want %v", left, want)
	}
}

func TestDryRun(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, tok := f.pushUser("America/Toronto")
	dead := f.token(u)
	h := f.household("Home", u)
	f.entry(h, "Milk", "full", today)
	f.sender.ticket = func(m expiry.Message) expiry.Ticket {
		if m.To == dead {
			return errorTicket(expiry.ErrorDeviceNotRegistered)
		}
		return expiry.Ticket{Status: expiry.TicketOK}
	}

	sum, err := f.runErr(torontoNine, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.sender.batches) != 0 {
		t.Errorf("dry run sent %d batches", len(f.sender.batches))
	}
	if f.notifications(u) != 0 || f.count(`SELECT count(*) FROM push_tokens WHERE user_id = $1`, u) != 2 {
		t.Error("dry run wrote to the database")
	}
	logs := f.logs.String()
	if !strings.Contains(logs, `"msg":"dry run: would send expiry push","user_id":"`+u+`","household_id":"`+h+`","title":"Expiring soon in Home","body":"Milk"`) {
		t.Errorf("dry run did not log the push:\n%s", logs)
	}
	if strings.Contains(logs, tok) {
		t.Errorf("dry run logged a token:\n%s", logs)
	}
	if sum.UsersProcessed != 1 || sum.PushesSent != 0 {
		t.Errorf("summary %+v", sum)
	}

	// A real run afterwards still sends.
	f.run(torontoNine)
	if got := f.sender.sent(); len(got) != 2 {
		t.Errorf("real run after the dry run sent %d messages, want 2", len(got))
	}
}

func TestRunsAsAppRole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, _ := f.pushUser("America/Toronto")
	f.entry(f.household("Home", u), "Milk", "full", today)
	// Statements the job runs as the pool's own (superuser) role would ignore
	// this; as shelfkin_app, which has no INSERT grant on
	// expiry_notifications without it, recording fails.
	f.exec(`REVOKE INSERT ON expiry_notifications FROM shelfkin_app`)

	sum, err := f.runErr(torontoNine, false)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failures != 1 || f.notifications(u) != 0 {
		t.Errorf("recording as shelfkin_app should fail without the grant: summary %+v", sum)
	}
	if !strings.Contains(f.logs.String(), "permission denied") {
		t.Errorf("no permission error logged:\n%s", f.logs)
	}
}
