-- push_tokens has no row-level security (#10): every query here filters by,
-- or writes, the caller's user ID. Run them with dbtx.WithUser.

-- name: UpsertPushToken :one
-- Registers token for the caller. A token the caller already has gets a new
-- last_seen_at (and platform); a token another user has moves to the caller,
-- since token is unique. Either way there is one row per token.
INSERT INTO push_tokens (user_id, token, platform)
VALUES (@user_id, @token, @platform)
ON CONFLICT (token) DO UPDATE
SET user_id      = EXCLUDED.user_id,
    platform     = EXCLUDED.platform,
    last_seen_at = now()
RETURNING token, platform, last_seen_at;

-- name: DeleteUserPushTokenByValue :exec
-- Deletes the caller's token by its value (DELETE /me/push-tokens/{token}).
-- Another user's token, or an unknown one, matches no row.
DELETE FROM push_tokens WHERE user_id = @user_id AND token = @token;
