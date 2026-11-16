-- Expo push tickets waiting for their receipt (#47). See docs/data-model.md
-- (push_tickets).
--
-- When Expo accepts a push it returns a ticket ID; about 15 minutes later the
-- receipt for that ID says whether the push was delivered to Apple or Google.
-- The expiry job (#22) stores each accepted ticket here with the token it was
-- sent to, and on its next runs fetches the receipts: DeviceNotRegistered
-- deletes the token, and a row is deleted once its receipt is processed or
-- after 24 hours without one.
--
-- token_id cascades, so deleting a token (by the user, or after a
-- DeviceNotRegistered receipt) deletes its pending tickets too. Like
-- push_tokens, the table has no row-level security; only the job uses it.

-- +goose Up
CREATE TABLE push_tickets (
    -- Expo's ticket ID.
    id       text        PRIMARY KEY,
    token_id uuid        NOT NULL REFERENCES push_tokens (id) ON DELETE CASCADE,
    -- Set by the job to its run time, not by now(), so receipts and age-outs
    -- are timed against the same clock as the run (--now).
    sent_at  timestamptz NOT NULL DEFAULT now()
);

-- For the ON DELETE CASCADE from push_tokens.
CREATE INDEX push_tickets_token_id ON push_tickets (token_id);
-- For finding tickets old enough for a receipt, and ones that aged out.
CREATE INDEX push_tickets_sent_at ON push_tickets (sent_at);

GRANT SELECT, INSERT, DELETE ON push_tickets TO shelfkin_app;

-- +goose Down
DROP TABLE push_tickets;
