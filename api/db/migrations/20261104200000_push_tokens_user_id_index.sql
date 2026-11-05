-- Index push_tokens by user. Postgres does not index foreign-key columns on
-- its own; without this, every "tokens for this user" query (#21, #22) and the
-- ON DELETE CASCADE from users (#46) scans the whole table.
-- See docs/data-model.md (push_tokens).

-- +goose Up
CREATE INDEX push_tokens_user_id ON push_tokens (user_id);

-- +goose Down
DROP INDEX push_tokens_user_id;
