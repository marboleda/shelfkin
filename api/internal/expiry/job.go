// Package expiry is the expiry notification job (#22): run hourly, it sends
// each opted-in user whose local time is 9am one summary push per household
// listing the items there that expire soon. Each run first reads Expo's
// receipts for earlier pushes and prunes dead and stale push tokens (#47).
// See docs/data-model.md (Expiry rules) and api/cmd/expiry-job.
package expiry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	// Embed the time zone database so users.timezone can be loaded on
	// images without /usr/share/zoneinfo (and on Windows).
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
)

// NotifyHour is the local hour a user's pushes are sent in.
const NotifyHour = 9

// SoonDays is the "expiring soon" window: expires_on <= local today + 3 days.
const SoonDays = 3

// RetentionDays is how long expiry_notifications rows are kept after their
// expiry date.
const RetentionDays = 30

// maxNamesInBody is how many item names a push body lists.
const maxNamesInBody = 3

// ReceiptDelay is how old a push ticket must be before the job asks Expo for
// its receipt; Expo recommends waiting about 15 minutes.
const ReceiptDelay = 15 * time.Minute

// TicketMaxAge is how long a ticket is kept waiting for a receipt that Expo
// never returns.
const TicketMaxAge = 24 * time.Hour

// StaleTokenAge is how long a push token may go without its last_seen_at
// being refreshed (the app does it on start, #34) before it is deleted.
const StaleTokenAge = 90 * 24 * time.Hour

// Job is one run of the expiry notification job.
type Job struct {
	// DB is the pool; every transaction goes through dbtx as shelfkin_app.
	DB     dbtx.Beginner
	Sender Sender
	Logger *slog.Logger
	// DryRun logs what would be sent and deleted, and sends and writes
	// nothing.
	DryRun bool
}

// Summary counts what a run did.
type Summary struct {
	// UsersProcessed is the number of users whose local hour was NotifyHour.
	UsersProcessed int
	// PushesSent is the number of messages Expo accepted (one per household
	// per token).
	PushesSent int
	// Failures is the number of users whose processing failed.
	Failures int
	// ReceiptsProcessed is the number of push receipts read from Expo and
	// acted on.
	ReceiptsProcessed int
	// TokensDeleted is the number of push tokens deleted because a receipt
	// said DeviceNotRegistered or because they were stale.
	TokensDeleted int
}

// Run runs the job once as of now. It logs one summary line at the end. It
// returns an error if a step not tied to one user failed (listing users,
// deleting old rows); a user's failure is counted in Summary.Failures and does
// not stop the others.
func (j *Job) Run(ctx context.Context, now time.Time) (Summary, error) {
	var sum Summary
	var errs []error

	// Receipts first, so tokens Expo has given up on get no new pushes.
	if err := j.processReceipts(ctx, now, &sum); err != nil {
		j.Logger.Error("processing push receipts", "error", err)
		errs = append(errs, err)
	}
	if err := j.pruneStaleTokens(ctx, now, &sum); err != nil {
		j.Logger.Error("pruning stale push tokens", "error", err)
		errs = append(errs, err)
	}
	if err := j.deleteOld(ctx, now); err != nil {
		j.Logger.Error("deleting old expiry notifications", "error", err)
		errs = append(errs, err)
	}

	var recipients []db.ListPushRecipientsRow
	err := dbtx.WithoutUser(ctx, j.DB, func(tx pgx.Tx) error {
		var err error
		recipients, err = db.New(tx).ListPushRecipients(ctx)
		return err
	})
	if err != nil {
		j.Logger.Error("listing push recipients", "error", err)
		errs = append(errs, err)
	}

	for _, r := range recipients {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		loc := j.location(r)
		local := now.In(loc)
		if local.Hour() != NotifyHour {
			continue
		}
		sum.UsersProcessed++
		sent, err := j.processUser(ctx, r.ID, local)
		sum.PushesSent += sent
		if err != nil {
			sum.Failures++
			j.Logger.Error("expiry notifications failed for user", "user_id", uuidString(r.ID), "error", err)
		}
	}

	attrs := []any{
		"now", now.UTC().Format(time.RFC3339),
		"dry_run", j.DryRun,
		"users_processed", sum.UsersProcessed,
		"pushes_sent", sum.PushesSent,
		"failures", sum.Failures,
		"receipts_processed", sum.ReceiptsProcessed,
		"tokens_deleted", sum.TokensDeleted,
	}
	err = errors.Join(errs...)
	if err != nil || sum.Failures > 0 {
		if err != nil {
			attrs = append(attrs, "error", err)
		}
		j.Logger.Error("expiry job finished", attrs...)
	} else {
		j.Logger.Info("expiry job finished", attrs...)
	}
	return sum, err
}

// location loads the user's time zone, falling back to UTC with a warning.
func (j *Job) location(r db.ListPushRecipientsRow) *time.Location {
	// LoadLocation treats "" as UTC and "Local" as the server's zone; neither
	// is a valid users.timezone.
	if r.Timezone != "" && r.Timezone != "Local" {
		if loc, err := time.LoadLocation(r.Timezone); err == nil {
			return loc
		}
	}
	j.Logger.Warn("unknown time zone, using UTC", "user_id", uuidString(r.ID), "timezone", r.Timezone)
	return time.UTC
}

// deleteOld deletes expiry_notifications rows whose expiry date is more than
// RetentionDays before today (UTC).
func (j *Job) deleteOld(ctx context.Context, now time.Time) error {
	before := toDate(dateOf(now.UTC()).AddDate(0, 0, -RetentionDays))
	return dbtx.WithoutUser(ctx, j.DB, func(tx pgx.Tx) error {
		q := db.New(tx)
		if j.DryRun {
			n, err := q.CountOldExpiryNotifications(ctx, before)
			if err == nil {
				j.Logger.Info("dry run: would delete old expiry notifications", "rows", n)
			}
			return err
		}
		n, err := q.DeleteOldExpiryNotifications(ctx, before)
		if err == nil {
			j.Logger.Info("deleted old expiry notifications", "rows", n)
		}
		return err
	})
}

// processReceipts fetches the receipts for the tickets sent at least
// ReceiptDelay before now, in batches of MaxReceiptBatchSize. A receipt with
// DeviceNotRegistered deletes the ticket's token; any other receipt error is
// logged with the ticket ID. Each ticket with a receipt is then deleted, and
// so is every ticket older than TicketMaxAge, receipt or not. A batch Expo
// fails to answer is left for the next run.
func (j *Job) processReceipts(ctx context.Context, now time.Time, sum *Summary) error {
	dueBefore := toTimestamp(now.Add(-ReceiptDelay))
	expiredBefore := toTimestamp(now.Add(-TicketMaxAge))

	var due []db.PushTicket
	err := dbtx.WithoutUser(ctx, j.DB, func(tx pgx.Tx) error {
		var err error
		due, err = db.New(tx).ListDuePushTickets(ctx, dueBefore)
		return err
	})
	if err != nil {
		return fmt.Errorf("listing push tickets: %w", err)
	}

	if j.DryRun {
		return dbtx.WithoutUser(ctx, j.DB, func(tx pgx.Tx) error {
			n, err := db.New(tx).CountExpiredPushTickets(ctx, expiredBefore)
			if err == nil {
				j.Logger.Info("dry run: would fetch push receipts and delete expired push tickets",
					"tickets", len(due), "expired_tickets", n)
			}
			return err
		})
	}

	var errs []error
	for start := 0; start < len(due); start += MaxReceiptBatchSize {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		batch := due[start:min(start+MaxReceiptBatchSize, len(due))]
		ids := make([]string, len(batch))
		for i, t := range batch {
			ids[i] = t.ID
		}
		receipts, err := j.Sender.Receipts(ctx, ids)
		if err != nil {
			errs = append(errs, fmt.Errorf("fetching push receipts: %w", err))
			continue
		}

		var done []string
		var dead []pgtype.UUID
		for _, t := range batch {
			r, ok := receipts[t.ID]
			if !ok {
				// Not ready yet, or never coming; it ages out below.
				continue
			}
			done = append(done, t.ID)
			switch {
			case r.Status == TicketOK:
			case r.Details.Error == ErrorDeviceNotRegistered:
				dead = append(dead, t.TokenID)
			default:
				code := r.Details.Error
				if code == "" {
					code = "unknown"
				}
				// Never log r.Message: it can quote the push token.
				j.Logger.Warn("push receipt error", "ticket_id", t.ID, "error_code", code)
			}
		}
		if len(done) == 0 {
			continue
		}
		var deleted int64
		err = dbtx.WithoutUser(ctx, j.DB, func(tx pgx.Tx) error {
			q := db.New(tx)
			if len(dead) > 0 {
				var err error
				if deleted, err = q.DeletePushTokensByID(ctx, dead); err != nil {
					return fmt.Errorf("deleting unregistered push tokens: %w", err)
				}
			}
			if err := q.DeletePushTickets(ctx, done); err != nil {
				return fmt.Errorf("deleting processed push tickets: %w", err)
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sum.ReceiptsProcessed += len(done)
		sum.TokensDeleted += int(deleted)
		if deleted > 0 {
			j.Logger.Info("deleted push tokens with DeviceNotRegistered receipts", "count", deleted)
		}
	}

	err = dbtx.WithoutUser(ctx, j.DB, func(tx pgx.Tx) error {
		n, err := db.New(tx).DeleteExpiredPushTickets(ctx, expiredBefore)
		if err == nil && n > 0 {
			j.Logger.Info("deleted push tickets with no receipt after 24 hours", "rows", n)
		}
		return err
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("deleting expired push tickets: %w", err))
	}
	return errors.Join(errs...)
}

// pruneStaleTokens deletes push tokens whose last_seen_at is more than
// StaleTokenAge before now.
func (j *Job) pruneStaleTokens(ctx context.Context, now time.Time, sum *Summary) error {
	before := toTimestamp(now.Add(-StaleTokenAge))
	return dbtx.WithoutUser(ctx, j.DB, func(tx pgx.Tx) error {
		q := db.New(tx)
		if j.DryRun {
			n, err := q.CountStalePushTokens(ctx, before)
			if err == nil {
				j.Logger.Info("dry run: would delete stale push tokens", "rows", n)
			}
			return err
		}
		n, err := q.DeleteStalePushTokens(ctx, before)
		if err != nil {
			return err
		}
		sum.TokensDeleted += int(n)
		j.Logger.Info("deleted stale push tokens", "rows", n)
		return nil
	})
}

// householdPush is the summary push for one household.
type householdPush struct {
	householdID pgtype.UUID
	title, body string
	entries     []db.ListExpiringEntriesRow
	// accepted is set once Expo accepts the push for one of the user's tokens.
	accepted bool
	// errorCodes are the ticket errors, other than DeviceNotRegistered, that
	// Expo returned for this push.
	errorCodes []string
	// sendFailed is set when a batch with this push in it failed outright.
	sendFailed bool
}

// processUser sends userID's pushes; local is the run's time in the user's
// zone. It returns the number of messages Expo accepted.
func (j *Job) processUser(ctx context.Context, userID pgtype.UUID, local time.Time) (int, error) {
	cutoff := toDate(dateOf(local).AddDate(0, 0, SoonDays))
	var tokens []db.ListUserPushTokensRow
	var entries []db.ListExpiringEntriesRow
	err := dbtx.WithUser(ctx, j.DB, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		var err error
		if tokens, err = q.ListUserPushTokens(ctx, userID); err != nil {
			return err
		}
		entries, err = q.ListExpiringEntries(ctx, db.ListExpiringEntriesParams{UserID: userID, Cutoff: cutoff})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("reading entries: %w", err)
	}
	if len(tokens) == 0 || len(entries) == 0 {
		return 0, nil
	}

	pushes := buildPushes(entries)
	if j.DryRun {
		for _, p := range pushes {
			j.Logger.Info("dry run: would send expiry push",
				"user_id", uuidString(userID),
				"household_id", uuidString(p.householdID),
				"title", p.title,
				"body", p.body,
				"entries", len(p.entries),
				"tokens", len(tokens))
		}
		return 0, nil
	}

	// One message per (household, token). owner[i] is the push message i
	// belongs to and tokenOf[i] its token's index.
	var msgs []Message
	var owner, tokenOf []int
	for pi, p := range pushes {
		for ti, t := range tokens {
			msgs = append(msgs, Message{
				To:    t.Token,
				Title: p.title,
				Body:  p.body,
				Data:  map[string]string{"householdId": uuidString(p.householdID)},
				Sound: "default",
			})
			owner = append(owner, pi)
			tokenOf = append(tokenOf, ti)
		}
	}

	var errs []error
	sent := 0
	deadTokens := map[int]bool{}
	// The tickets of accepted messages and the tokens they went to, kept so
	// a later run can fetch their receipts.
	var ticketIDs []string
	var ticketTokens []pgtype.UUID
	for start := 0; start < len(msgs); start += MaxBatchSize {
		end := min(start+MaxBatchSize, len(msgs))
		tickets, err := j.Sender.Send(ctx, msgs[start:end])
		if err == nil && len(tickets) != end-start {
			err = fmt.Errorf("expo push: got %d tickets for %d messages", len(tickets), end-start)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("sending: %w", err))
			for i := start; i < end; i++ {
				pushes[owner[i]].sendFailed = true
			}
			continue
		}
		for k, t := range tickets {
			i := start + k
			p := &pushes[owner[i]]
			switch {
			case t.Status == TicketOK:
				p.accepted = true
				sent++
				if t.ID != "" {
					ticketIDs = append(ticketIDs, t.ID)
					ticketTokens = append(ticketTokens, tokens[tokenOf[i]].ID)
				}
			case t.Details.Error == ErrorDeviceNotRegistered:
				deadTokens[tokenOf[i]] = true
			default:
				code := t.Details.Error
				if code == "" {
					code = "unknown"
				}
				p.errorCodes = append(p.errorCodes, code)
			}
		}
	}

	for _, p := range pushes {
		if !p.accepted && !p.sendFailed && len(p.errorCodes) > 0 {
			errs = append(errs, fmt.Errorf("expo rejected the push for household %s on every token: %s",
				uuidString(p.householdID), strings.Join(p.errorCodes, ", ")))
		}
	}

	err = dbtx.WithUser(ctx, j.DB, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if len(ticketIDs) > 0 {
			if err := q.RecordPushTickets(ctx, db.RecordPushTicketsParams{
				Ids: ticketIDs, TokenIds: ticketTokens, SentAt: toTimestamp(local),
			}); err != nil {
				return fmt.Errorf("recording push tickets: %w", err)
			}
		}
		for ti := range deadTokens {
			if err := q.DeletePushToken(ctx, db.DeletePushTokenParams{UserID: userID, ID: tokens[ti].ID}); err != nil {
				return fmt.Errorf("deleting unregistered token: %w", err)
			}
		}
		var ids []pgtype.UUID
		var dates []pgtype.Date
		for _, p := range pushes {
			if !p.accepted {
				continue
			}
			for _, e := range p.entries {
				ids = append(ids, e.EntryID)
				dates = append(dates, e.ExpiresOn)
			}
		}
		if len(ids) == 0 {
			return nil
		}
		if err := q.RecordExpiryNotifications(ctx, db.RecordExpiryNotificationsParams{
			EntryIds: ids, UserID: userID, ExpiresOns: dates,
		}); err != nil {
			return fmt.Errorf("recording notifications: %w", err)
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	if len(deadTokens) > 0 && err == nil {
		j.Logger.Info("deleted unregistered push tokens", "user_id", uuidString(userID), "count", len(deadTokens))
	}
	return sent, errors.Join(errs...)
}

// buildPushes groups entries (ordered by household, then soonest expiry) into
// one push per household.
func buildPushes(entries []db.ListExpiringEntriesRow) []householdPush {
	var pushes []householdPush
	for _, e := range entries {
		if len(pushes) == 0 || pushes[len(pushes)-1].householdID != e.HouseholdID {
			pushes = append(pushes, householdPush{
				householdID: e.HouseholdID,
				title:       Title(e.HouseholdName),
			})
		}
		p := &pushes[len(pushes)-1]
		p.entries = append(p.entries, e)
	}
	for i := range pushes {
		names := make([]string, len(pushes[i].entries))
		for k, e := range pushes[i].entries {
			names[k] = e.ItemName
		}
		pushes[i].body = Body(names)
	}
	return pushes
}

// Title is the push title for a household.
func Title(householdName string) string {
	return "Expiring soon in " + householdName
}

// Body lists up to three item names, soonest expiry first, then "and N more":
// "Milk, Eggs, Spinach and 2 more". names must already be in expiry order. A
// name that appears more than once (two entries of the same item) is listed
// once and counted once.
func Body(names []string) string {
	var distinct []string
	seen := map[string]bool{}
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			distinct = append(distinct, n)
		}
	}
	shown := distinct[:min(len(distinct), maxNamesInBody)]
	body := strings.Join(shown, ", ")
	if more := len(distinct) - len(shown); more > 0 {
		body += " and " + strconv.Itoa(more) + " more"
	}
	return body
}

// dateOf returns t's calendar date as midnight UTC, so AddDate moves whole
// days.
func dateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func toTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func toDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	v, _ := u.Value()
	s, _ := v.(string)
	return s
}
