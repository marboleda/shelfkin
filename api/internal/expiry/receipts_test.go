package expiry_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marboleda/shelfkin/api/internal/expiry"
)

// receiptsNow is a run time at which nobody in these tests is at 9am local
// (they are all in UTC), so only receipts and pruning happen.
const receiptsNow = "2026-10-27T05:00:00Z"

// ago returns receiptsNow minus d, for timestamptz parameters.
func ago(d time.Duration) time.Time {
	t, err := time.Parse(time.RFC3339, receiptsNow)
	if err != nil {
		panic(err)
	}
	return t.Add(-d)
}

func (f *fixture) tokenID(token string) string {
	f.t.Helper()
	return f.id(`SELECT id::text FROM push_tokens WHERE token = $1`, token)
}

// ticket stores a pending Expo ticket for token, sent at sentAt.
func (f *fixture) ticket(id, token string, sentAt time.Time) {
	f.t.Helper()
	f.exec(`INSERT INTO push_tickets (id, token_id, sent_at) VALUES ($1, $2, $3)`, id, f.tokenID(token), sentAt)
}

func (f *fixture) tickets() []string {
	f.t.Helper()
	rows, err := f.pool.Query(f.t.Context(), `SELECT id FROM push_tickets ORDER BY id`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			f.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return ids
}

func (f *fixture) hasToken(token string) bool {
	f.t.Helper()
	return f.count(`SELECT count(*) FROM push_tokens WHERE token = $1`, token) == 1
}

func errorReceipt(code string) expiry.Receipt {
	r := expiry.Receipt{Status: expiry.TicketError, Message: "rejected ExponentPushToken[quoted]"}
	r.Details.Error = code
	return r
}

func TestOKReceiptDeletesTicketAndKeepsToken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, tok := f.pushUser("UTC")
	f.ticket("ok-ticket", tok, ago(20*time.Minute))

	sum := f.run(receiptsNow)
	if want := [][]string{{"ok-ticket"}}; !slices.EqualFunc(f.sender.receiptReq, want, slices.Equal) {
		t.Errorf("receipt requests %v, want %v", f.sender.receiptReq, want)
	}
	if got := f.tickets(); len(got) != 0 {
		t.Errorf("tickets left %v, want none", got)
	}
	if !f.hasToken(tok) {
		t.Error("an ok receipt deleted the token")
	}
	if sum.ReceiptsProcessed != 1 || sum.TokensDeleted != 0 {
		t.Errorf("summary %+v", sum)
	}
	if !strings.Contains(f.logs.String(), `"receipts_processed":1,"tokens_deleted":0`) {
		t.Errorf("summary line does not count the receipt:\n%s", f.logs)
	}
}

func TestDeviceNotRegisteredReceiptDeletesToken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, dead := f.pushUser("UTC")
	alive := f.token(u)
	f.ticket("dead-1", dead, ago(30*time.Minute))
	f.ticket("dead-2", dead, ago(20*time.Minute))
	f.ticket("alive-1", alive, ago(20*time.Minute))
	f.sender.receipt = func(id string) (expiry.Receipt, bool) {
		if strings.HasPrefix(id, "dead-") {
			return errorReceipt(expiry.ErrorDeviceNotRegistered), true
		}
		return expiry.Receipt{Status: expiry.TicketOK}, true
	}

	sum := f.run(receiptsNow)
	if f.hasToken(dead) {
		t.Error("the DeviceNotRegistered token was not deleted")
	}
	if !f.hasToken(alive) {
		t.Error("the other token was deleted")
	}
	if got := f.tickets(); len(got) != 0 {
		t.Errorf("tickets left %v, want none", got)
	}
	if sum.ReceiptsProcessed != 3 || sum.TokensDeleted != 1 {
		t.Errorf("summary %+v", sum)
	}
	if strings.Contains(f.logs.String(), dead) || strings.Contains(f.logs.String(), "quoted") {
		t.Errorf("logs contain a token:\n%s", f.logs)
	}
}

func TestOtherReceiptErrorsAreLoggedAndKeepToken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, tok := f.pushUser("UTC")
	f.ticket("rate-ticket", tok, ago(20*time.Minute))
	f.ticket("blank-ticket", tok, ago(20*time.Minute))
	f.sender.receipt = func(id string) (expiry.Receipt, bool) {
		if id == "rate-ticket" {
			return errorReceipt("MessageRateExceeded"), true
		}
		return errorReceipt(""), true
	}

	sum := f.run(receiptsNow)
	if !f.hasToken(tok) {
		t.Error("a receipt error other than DeviceNotRegistered deleted the token")
	}
	if got := f.tickets(); len(got) != 0 {
		t.Errorf("tickets left %v, want none: a processed receipt deletes its ticket", got)
	}
	logs := f.logs.String()
	for _, want := range []string{
		`"msg":"push receipt error","ticket_id":"rate-ticket","error_code":"MessageRateExceeded"`,
		`"msg":"push receipt error","ticket_id":"blank-ticket","error_code":"unknown"`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %s:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, tok) || strings.Contains(logs, "quoted") {
		t.Errorf("logs contain a token:\n%s", logs)
	}
	if sum.ReceiptsProcessed != 2 || sum.TokensDeleted != 0 {
		t.Errorf("summary %+v", sum)
	}
}

func TestMissingReceiptAgesOut(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, tok := f.pushUser("UTC")
	f.ticket("fresh", tok, ago(10*time.Minute))            // too young to ask about
	f.ticket("waiting", tok, ago(2*time.Hour))             // no receipt yet; kept
	f.ticket("almost", tok, ago(24*time.Hour-time.Second)) // just under 24h; kept
	f.ticket("expired", tok, ago(25*time.Hour))            // no receipt after 24h; deleted
	f.sender.receipt = func(string) (expiry.Receipt, bool) { return expiry.Receipt{}, false }

	sum := f.run(receiptsNow)
	if len(f.sender.receiptReq) != 1 {
		t.Fatalf("receipt requests %v, want 1", f.sender.receiptReq)
	}
	if got, want := sorted(f.sender.receiptReq[0]...), sorted("waiting", "almost", "expired"); !slices.Equal(got, want) {
		t.Errorf("asked for receipts %v, want %v (not the ticket under 15 minutes old)", got, want)
	}
	if got, want := f.tickets(), []string{"almost", "fresh", "waiting"}; !slices.Equal(got, want) {
		t.Errorf("tickets left %v, want %v", got, want)
	}
	if !f.hasToken(tok) {
		t.Error("a missing receipt deleted the token")
	}
	if sum.ReceiptsProcessed != 0 {
		t.Errorf("summary %+v", sum)
	}
	if !strings.Contains(f.logs.String(), `"msg":"deleted push tickets with no receipt after 24 hours","rows":1`) {
		t.Errorf("age-out not logged:\n%s", f.logs)
	}
}

func TestReceiptsAreFetchedInBatchesOf1000(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, tok := f.pushUser("UTC")
	f.exec(`INSERT INTO push_tickets (id, token_id, sent_at)
		SELECT 'ticket-' || n, $1, $2 FROM generate_series(1, 2500) AS n`, f.tokenID(tok), ago(time.Hour))

	sum := f.run(receiptsNow)
	var sizes []int
	seen := map[string]bool{}
	for _, req := range f.sender.receiptReq {
		sizes = append(sizes, len(req))
		for _, id := range req {
			seen[id] = true
		}
	}
	if !slices.Equal(sizes, []int{1000, 1000, 500}) {
		t.Errorf("receipt request sizes %v, want [1000 1000 500]", sizes)
	}
	if len(seen) != 2500 {
		t.Errorf("asked for %d distinct tickets, want 2500", len(seen))
	}
	if n := len(f.tickets()); n != 0 {
		t.Errorf("%d tickets left, want 0", n)
	}
	if sum.ReceiptsProcessed != 2500 {
		t.Errorf("summary %+v", sum)
	}
}

func TestReceiptFetchErrorKeepsTickets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, tok := f.pushUser("UTC")
	f.ticket("waiting", tok, ago(time.Hour))
	f.ticket("expired", tok, ago(30*time.Hour))
	f.sender.receiptErr = errors.New("fake: HTTP 503")

	_, err := f.runErr(receiptsNow, false)
	if err == nil || !strings.Contains(err.Error(), "fetching push receipts") {
		t.Errorf("run error %v, want the receipts error", err)
	}
	if got, want := f.tickets(), []string{"waiting"}; !slices.Equal(got, want) {
		t.Errorf("tickets left %v, want %v (kept for the next run; the expired one still ages out)", got, want)
	}
	if !f.hasToken(tok) {
		t.Error("token deleted")
	}
}

func TestStaleTokensArePruned(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, stale := f.pushUser("UTC")
	recent := f.token(u)
	f.exec(`UPDATE push_tokens SET last_seen_at = $2 WHERE token = $1`, stale, ago(91*24*time.Hour))
	f.exec(`UPDATE push_tokens SET last_seen_at = $2 WHERE token = $1`, recent, ago(89*24*time.Hour))
	f.ticket("stale-ticket", stale, ago(time.Minute))

	sum := f.run(receiptsNow)
	if f.hasToken(stale) {
		t.Error("the token last seen 91 days ago was not deleted")
	}
	if !f.hasToken(recent) {
		t.Error("the token last seen 89 days ago was deleted")
	}
	if got := f.tickets(); len(got) != 0 {
		t.Errorf("tickets left %v: the stale token's tickets should go with it", got)
	}
	if sum.TokensDeleted != 1 {
		t.Errorf("summary %+v", sum)
	}
	if !strings.Contains(f.logs.String(), `"msg":"deleted stale push tokens","rows":1`) {
		t.Errorf("pruning not logged:\n%s", f.logs)
	}
}

func TestSendingStoresTickets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, tok := f.pushUser("America/Toronto")
	dead := f.token(u)
	failed := f.token(u)
	f.entry(f.household("Home", u), "Milk", "full", today)
	f.entry(f.household("Cabin", u), "Eggs", "full", today)
	n := 0
	f.sender.ticket = func(m expiry.Message) expiry.Ticket {
		switch m.To {
		case dead:
			return errorTicket(expiry.ErrorDeviceNotRegistered)
		case failed:
			return errorTicket("MessageTooBig")
		}
		n++
		return expiry.Ticket{Status: expiry.TicketOK, ID: "ticket-" + string(rune('a'+n-1))}
	}

	f.run(torontoNine)
	if got, want := f.tickets(), []string{"ticket-a", "ticket-b"}; !slices.Equal(got, want) {
		t.Fatalf("stored tickets %v, want %v (one per accepted message)", got, want)
	}
	if c := f.count(`SELECT count(*) FROM push_tickets k JOIN push_tokens t ON t.id = k.token_id
		WHERE t.token = $1 AND k.sent_at = $2::timestamptz`, tok, torontoNine); c != 2 {
		t.Errorf("%d tickets stored with the accepted token and the run's time, want 2", c)
	}

	// 15 minutes later their receipts are fetched; earlier they are not.
	f.run("2026-10-27T13:14:00Z")
	if len(f.sender.receiptReq) != 0 {
		t.Errorf("receipts requested after 14 minutes: %v", f.sender.receiptReq)
	}
	f.run("2026-10-27T13:15:00Z")
	if len(f.sender.receiptReq) != 1 || !slices.Equal(sorted(f.sender.receiptReq[0]...), []string{"ticket-a", "ticket-b"}) {
		t.Errorf("receipt requests %v, want one for both tickets", f.sender.receiptReq)
	}
	if got := f.tickets(); len(got) != 0 {
		t.Errorf("tickets left %v", got)
	}
}

func TestReceiptsDryRun(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, tok := f.pushUser("UTC")
	f.exec(`UPDATE push_tokens SET last_seen_at = $2 WHERE token = $1`, tok, ago(100*24*time.Hour))
	f.ticket("waiting", tok, ago(time.Hour))
	f.ticket("expired", tok, ago(30*time.Hour))

	if _, err := f.runErr(receiptsNow, true); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.receiptReq) != 0 {
		t.Errorf("dry run requested receipts: %v", f.sender.receiptReq)
	}
	if !f.hasToken(tok) || len(f.tickets()) != 2 {
		t.Error("dry run deleted rows")
	}
	logs := f.logs.String()
	for _, want := range []string{
		`"msg":"dry run: would fetch push receipts and delete expired push tickets","tickets":2,"expired_tickets":1`,
		`"msg":"dry run: would delete stale push tokens","rows":1`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %s:\n%s", want, logs)
		}
	}
}

func TestReceiptsRunAsAppRole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, tok := f.pushUser("UTC")
	f.ticket("ok-ticket", tok, ago(time.Hour))
	f.exec(`REVOKE DELETE ON push_tickets FROM shelfkin_app`)

	if _, err := f.runErr(receiptsNow, false); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("run error %v, want permission denied without the DELETE grant", err)
	}
	if got := f.tickets(); !slices.Equal(got, []string{"ok-ticket"}) {
		t.Errorf("tickets left %v", got)
	}
}
