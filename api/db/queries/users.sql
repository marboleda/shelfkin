-- name: GetUserIdentityByFirebaseUID :one
-- The user's ID and stored email. The auth middleware runs this, CreateUser
-- and UpdateUserEmail with dbtx.WithoutUser; GetUser and UpdateUserProfile
-- run with dbtx.WithUser.
SELECT id, email FROM users WHERE firebase_uid = $1;

-- name: CreateUser :one
-- Creates the user on first sight. When a concurrent request created the same
-- Firebase user first, this inserts nothing and returns no row; the caller
-- then reads the row the other request created.
INSERT INTO users (firebase_uid, email, display_name, timezone)
VALUES (@firebase_uid, @email, @display_name, 'UTC')
ON CONFLICT (firebase_uid) DO NOTHING
RETURNING id;

-- name: UpdateUserEmail :exec
-- Copies a changed email from the Firebase token. display_name is never
-- touched here.
UPDATE users SET email = @email, updated_at = now() WHERE id = @id;

-- name: GetUser :one
SELECT id, display_name, email, timezone, push_enabled
FROM users
WHERE id = $1;

-- name: UpdateUserProfile :one
-- A NULL argument leaves that column unchanged. updated_at is always set.
UPDATE users
SET display_name = COALESCE(sqlc.narg(display_name), display_name),
    timezone     = COALESCE(sqlc.narg(timezone), timezone),
    push_enabled = COALESCE(sqlc.narg(push_enabled), push_enabled),
    updated_at   = now()
WHERE id = @id
RETURNING id, display_name, email, timezone, push_enabled;

-- name: IsAccountDeleted :one
-- Whether the Firebase user's Shelfkin account was deleted within the last
-- day (see the delete_account migration). The auth middleware checks it
-- before creating a user, so an old ID token cannot recreate the account.
SELECT EXISTS (SELECT 1 FROM deleted_accounts WHERE firebase_uid = $1)::boolean AS deleted;

-- name: GetUserFirebaseUID :one
SELECT firebase_uid FROM users WHERE id = $1;

-- name: DeleteAccount :many
-- Deletes the caller (app.user_id) and the households they own alone, or,
-- when they own a household that has other members, deletes nothing and
-- returns those households. See the delete_account migration.
SELECT household_id::uuid AS household_id, household_name::text AS household_name
FROM public.delete_account();
