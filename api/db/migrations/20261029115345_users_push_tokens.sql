-- Users and the devices they receive push notifications on.
-- See docs/data-model.md (users, push_tokens).

-- +goose Up
CREATE TABLE users (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    firebase_uid text        NOT NULL UNIQUE,
    display_name text        NOT NULL,
    email        text,
    timezone     text        NOT NULL,
    push_enabled boolean     NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE push_tokens (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token        text        NOT NULL UNIQUE,
    platform     text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE push_tokens;
DROP TABLE users;
