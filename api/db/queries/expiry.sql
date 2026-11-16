-- Queries for the expiry notification job (api/cmd/expiry-job, #22). Every
-- one runs through api/internal/dbtx as shelfkin_app.

-- name: ListPushRecipients :many
-- ListPushRecipients returns every user who has opted in to pushes and has at
-- least one push token. The job filters them by local hour in Go, so a
-- timezone Postgres does not know cannot fail the query. users and
-- push_tokens have no row-level security, so this runs without a user.
SELECT u.id, u.timezone
FROM users u
WHERE u.push_enabled
  AND EXISTS (SELECT 1 FROM push_tokens t WHERE t.user_id = u.id)
ORDER BY u.id;

-- name: ListUserPushTokens :many
-- ListUserPushTokens returns a user's push tokens, oldest first.
SELECT id, token
FROM push_tokens
WHERE user_id = sqlc.arg(user_id)
ORDER BY created_at, id;

-- name: ListExpiringEntries :many
-- ListExpiringEntries returns the entries in the user's households that are
-- not out, expire on or before cutoff (the user's local today + 3 days) and
-- have not yet been announced to the user for their current expiry date.
-- Rows are grouped by household, soonest expiry first.
SELECT e.id            AS entry_id,
       e.household_id,
       h.name          AS household_name,
       c.name          AS item_name,
       e.expires_on::date AS expires_on
FROM pantry_entries e
JOIN household_members m
  ON m.household_id = e.household_id AND m.user_id = sqlc.arg(user_id)
JOIN households h ON h.id = e.household_id
JOIN catalog_items c ON c.id = e.catalog_item_id
WHERE e.level <> 'out'
  AND e.expires_on IS NOT NULL
  AND e.expires_on <= sqlc.arg(cutoff)::date
  AND NOT EXISTS (
      SELECT 1
      FROM expiry_notifications n
      WHERE n.entry_id = e.id
        AND n.user_id = sqlc.arg(user_id)
        AND n.expires_on = e.expires_on
  )
ORDER BY e.household_id, e.expires_on, lower(c.name), e.id;

-- name: RecordExpiryNotifications :exec
-- RecordExpiryNotifications records that the given entries were announced to
-- the user for the given expiry dates (entry_ids[i] with expires_ons[i]).
-- Rows that already exist are left alone.
INSERT INTO expiry_notifications (entry_id, user_id, expires_on)
-- The two unnest calls in one select list run in lockstep (pairing element i
-- of each array); the arrays always have the same length.
SELECT unnest(sqlc.arg(entry_ids)::uuid[]), sqlc.arg(user_id)::uuid, unnest(sqlc.arg(expires_ons)::date[])
ON CONFLICT (entry_id, user_id, expires_on) DO NOTHING;

-- name: DeletePushToken :exec
-- DeletePushToken deletes one of the user's push tokens, for example after
-- Expo reports DeviceNotRegistered.
DELETE FROM push_tokens
WHERE user_id = sqlc.arg(user_id) AND id = sqlc.arg(id);

-- name: DeleteOldExpiryNotifications :execrows
-- DeleteOldExpiryNotifications deletes every row whose expiry date is before
-- the given date. expiry_notifications has no row-level security.
DELETE FROM expiry_notifications
WHERE expires_on < sqlc.arg(before)::date;

-- name: CountOldExpiryNotifications :one
-- CountOldExpiryNotifications counts the rows DeleteOldExpiryNotifications
-- would delete, for dry runs.
SELECT count(*) FROM expiry_notifications
WHERE expires_on < sqlc.arg(before)::date;

-- Push receipts and stale tokens (#47). push_tickets and push_tokens have no
-- row-level security; these run without a user.

-- name: RecordPushTickets :exec
-- RecordPushTickets stores the tickets Expo returned for accepted pushes
-- (ids[i] was sent to token token_ids[i]) with the run's time. A token that
-- was deleted since it was read is skipped rather than failing the insert.
INSERT INTO push_tickets (id, token_id, sent_at)
SELECT k.id, k.token_id, sqlc.arg(sent_at)::timestamptz
FROM (
    -- The two unnest calls run in lockstep, as in RecordExpiryNotifications.
    SELECT unnest(sqlc.arg(ids)::text[]) AS id, unnest(sqlc.arg(token_ids)::uuid[]) AS token_id
) AS k
JOIN push_tokens t ON t.id = k.token_id
ON CONFLICT (id) DO NOTHING;

-- name: ListDuePushTickets :many
-- ListDuePushTickets returns the tickets sent at or before the given time
-- (the run's time minus 15 minutes), whose receipts Expo should have by now,
-- oldest first.
SELECT id, token_id, sent_at
FROM push_tickets
WHERE sent_at <= sqlc.arg(sent_before)::timestamptz
ORDER BY sent_at, id;

-- name: DeletePushTickets :exec
-- DeletePushTickets deletes the given tickets once their receipts have been
-- processed.
DELETE FROM push_tickets WHERE id = ANY (sqlc.arg(ids)::text[]);

-- name: DeletePushTokensByID :execrows
-- DeletePushTokensByID deletes tokens Expo reported as DeviceNotRegistered in
-- a receipt. Their tickets go with them (ON DELETE CASCADE).
DELETE FROM push_tokens WHERE id = ANY (sqlc.arg(ids)::uuid[]);

-- name: DeleteExpiredPushTickets :execrows
-- DeleteExpiredPushTickets deletes tickets sent before the given time (the
-- run's time minus 24 hours) whose receipt never came.
DELETE FROM push_tickets WHERE sent_at < sqlc.arg(sent_before)::timestamptz;

-- name: CountExpiredPushTickets :one
-- CountExpiredPushTickets counts the rows DeleteExpiredPushTickets would
-- delete, for dry runs.
SELECT count(*) FROM push_tickets WHERE sent_at < sqlc.arg(sent_before)::timestamptz;

-- name: DeleteStalePushTokens :execrows
-- DeleteStalePushTokens deletes tokens whose last_seen_at is before the given
-- time (the run's time minus 90 days): no device has refreshed them since.
DELETE FROM push_tokens WHERE last_seen_at < sqlc.arg(seen_before)::timestamptz;

-- name: CountStalePushTokens :one
-- CountStalePushTokens counts the rows DeleteStalePushTokens would delete,
-- for dry runs.
SELECT count(*) FROM push_tokens WHERE last_seen_at < sqlc.arg(seen_before)::timestamptz;
