// Package expiry is the expiry notification job (#22): run hourly, it sends
// each opted-in user whose local time is 9am one summary push per household
// listing the items there that expire soon. See docs/data-model.md (Expiry
// rules) and api/cmd/expiry-job.
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
}

// Run runs the job once as of now. It logs one summary line at the end. It
// returns an error if a step not tied to one user failed (listing users,
// deleting old rows); a user's failure is counted in Summary.Failures and does
// not stop the others.
func (j *Job) Run(ctx context.Context, now time.Time) (Summary, error) {
	var sum Summary
	var errs []error

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
