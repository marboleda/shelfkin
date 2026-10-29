-- Households, their members and their invite codes.
-- See docs/data-model.md (households, household_members, household_invites).
--
-- households.created_by and household_invites.created_by are nullable with no
-- ON DELETE action for now; #46 changes them to ON DELETE SET NULL.

-- +goose Up
CREATE TYPE member_role AS ENUM ('owner', 'member');

CREATE TABLE households (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    name       text        NOT NULL,
    created_by uuid        REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE household_members (
    household_id uuid        NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role         member_role NOT NULL,
    joined_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (household_id, user_id)
);

-- At most one owner per household. A plain (non-deferrable) unique index, so
-- it is checked on every statement: ownership transfer must demote the
-- current owner before promoting the new one.
CREATE UNIQUE INDEX household_members_one_owner
    ON household_members (household_id) WHERE role = 'owner';

-- "Which households am I in?"
CREATE INDEX household_members_user_id ON household_members (user_id);

CREATE TABLE household_invites (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    household_id uuid        NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    code         text        NOT NULL UNIQUE,
    created_by   uuid        REFERENCES users (id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz
);

-- +goose Down
DROP TABLE household_invites;
DROP TABLE household_members;
DROP TABLE households;
DROP TYPE member_role;
