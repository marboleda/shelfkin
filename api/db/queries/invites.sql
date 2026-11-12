-- Household invites (#16). Every query here runs with dbtx.WithUser. The
-- household_invites_owner policy (#10) limits household_invites to the
-- households the caller owns, behind the API's requireOwner check; JoinHousehold
-- goes through the join_household function instead.

-- name: CreateInvite :one
-- Inserts an invite that expires 7 days after it is created, both times from
-- the database clock. Returns no row if the code is already taken (by any
-- invite, active, expired or revoked): a failed INSERT would abort the
-- transaction, so the caller retries with a new code instead.
INSERT INTO household_invites (household_id, code, created_by, created_at, expires_at)
VALUES (@household_id, @code, @created_by, now(), now() + interval '7 days')
ON CONFLICT (code) DO NOTHING
RETURNING id, code, created_at, expires_at;

-- name: ListActiveInvites :many
-- The household's invites that are neither revoked nor expired, newest first.
SELECT id, code, created_at, expires_at
FROM household_invites
WHERE household_id = @household_id AND revoked_at IS NULL AND expires_at > now()
ORDER BY created_at DESC, id DESC;

-- name: RevokeInvite :execrows
-- Revokes one of the household's invites. Revoking again keeps the first
-- revoked_at. Affects no row if the invite does not exist or belongs to
-- another household.
UPDATE household_invites
SET revoked_at = COALESCE(revoked_at, now())
WHERE id = @id AND household_id = @household_id;

-- name: JoinHousehold :one
-- Adds the caller (app.user_id) to the household of an invite code; see the
-- join_household migration for the statuses.
SELECT j.household_id::uuid AS household_id, j.status::text AS status
FROM public.join_household(@code::text) AS j;
