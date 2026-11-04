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
    updated_at   = now()
WHERE id = @id
RETURNING id, display_name, email, timezone, push_enabled;
