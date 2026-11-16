-- Deleting a user's account (DELETE /me, #46). See docs/data-model.md
-- (users, deleted_accounts, Access control).
--
-- 1. households.created_by, household_invites.created_by and
--    catalog_items.created_by become ON DELETE SET NULL (they had no action,
--    #5 and #7), so a user who created a household, an invite or a custom
--    item in a household that still exists can be deleted. The rows stay with
--    a NULL creator, like pantry_entries.created_by and item_events.actor_id.
--
-- 2. deleted_accounts remembers the Firebase UID of each deleted account for
--    a day. Firebase ID tokens are verified without a call to Firebase, so a
--    token issued before the account was deleted stays valid until it expires
--    (at most an hour). The API's auth middleware refuses to create a new
--    user for a UID listed here, so such a token gets 401 and does not
--    recreate the account. Rows older than a day are pruned by
--    delete_account itself; by then every token issued before the deletion
--    has expired.
--
-- 3. delete_account() deletes the caller (app.user_id) and the households
--    they own alone. shelfkin_app has no DELETE grant on users, and RLS stops
--    it from deleting an owner's own household_members row, so this is a
--    SECURITY DEFINER function owned by the migration user that runs in the
--    caller's transaction. The API deletes the Firebase user in the same
--    transaction after this returns, and rolls everything back if that fails.
--
--    Result: the households the caller owns that have other members, sorted
--    by name (case-insensitive), as (household_id, household_name). If there
--    are any, nothing is deleted: ownership must be transferred first.
--    Otherwise it returns no rows and, in the caller's transaction:
--      - deletes every household the caller owns (they are its only member),
--        which cascades to its members, invites, custom items, item defaults,
--        entries and events;
--      - deletes the users row, which cascades to the caller's
--        household_members, push_tokens (and their push_tickets) and
--        expiry_notifications, and sets created_by, updated_by and actor_id
--        to NULL wherever the caller is named in a household that remains;
--      - records the Firebase UID in deleted_accounts.
--
--    Errors (SQLSTATE):
--      42501 insufficient_privilege  app.user_id is not set
--      P0002 no_data_found           the caller has no users row (already
--                                    deleted)
--
--    Concurrency: the users row is locked first, then every household the
--    caller belongs to (FOR UPDATE, in ID order) before any membership is
--    read. transfer_ownership locks the household FOR UPDATE and
--    join_household FOR KEY SHARE, so a transfer to the caller or a join of
--    a household they own runs entirely before (and is seen) or after (and
--    finds the household gone). A member leaving a shared household
--    concurrently can at worst make this return the 409 rows once more.
--
--    The empty search_path (with every name schema-qualified) stops a caller
--    from shadowing the tables used here.

-- +goose Up
ALTER TABLE households
    DROP CONSTRAINT households_created_by_fkey,
    ADD CONSTRAINT households_created_by_fkey
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE household_invites
    DROP CONSTRAINT household_invites_created_by_fkey,
    ADD CONSTRAINT household_invites_created_by_fkey
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE catalog_items
    DROP CONSTRAINT catalog_items_created_by_fkey,
    ADD CONSTRAINT catalog_items_created_by_fkey
        FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;

CREATE TABLE deleted_accounts (
    firebase_uid text        PRIMARY KEY,
    deleted_at   timestamptz NOT NULL DEFAULT now()
);

-- For pruning rows older than a day.
CREATE INDEX deleted_accounts_deleted_at ON deleted_accounts (deleted_at);

-- The auth middleware checks it (WithoutUser) before creating a user. Like
-- users, it holds no household data and has no row-level security; only
-- delete_account writes it.
GRANT SELECT ON deleted_accounts TO shelfkin_app;

-- +goose StatementBegin
CREATE FUNCTION delete_account() RETURNS TABLE (household_id uuid, household_name text)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = ''
AS $$
#variable_conflict use_column
DECLARE
    caller uuid := NULLIF(pg_catalog.current_setting('app.user_id', true), '')::uuid;
    uid text;
BEGIN
    IF caller IS NULL THEN
        RAISE EXCEPTION 'delete_account: app.user_id is not set'
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    SELECT u.firebase_uid INTO uid
    FROM public.users u
    WHERE u.id = caller
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'delete_account: the caller has no account'
            USING ERRCODE = 'no_data_found';
    END IF;

    PERFORM 1
    FROM public.households h
    WHERE h.id IN (SELECT m.household_id FROM public.household_members m WHERE m.user_id = caller)
    ORDER BY h.id
    FOR UPDATE;

    RETURN QUERY
    SELECT h.id, h.name
    FROM public.households h
    JOIN public.household_members m
      ON m.household_id = h.id AND m.user_id = caller AND m.role = 'owner'
    WHERE EXISTS (
        SELECT 1 FROM public.household_members o
        WHERE o.household_id = h.id AND o.user_id <> caller
    )
    ORDER BY pg_catalog.lower(h.name), h.name, h.id;
    IF FOUND THEN
        RETURN;
    END IF;

    DELETE FROM public.households h
    WHERE h.id IN (
        SELECT m.household_id FROM public.household_members m
        WHERE m.user_id = caller AND m.role = 'owner'
    );

    DELETE FROM public.users u WHERE u.id = caller;

    INSERT INTO public.deleted_accounts (firebase_uid)
    VALUES (uid)
    ON CONFLICT (firebase_uid) DO UPDATE SET deleted_at = pg_catalog.now();

    DELETE FROM public.deleted_accounts d
    WHERE d.deleted_at < pg_catalog.now() - interval '1 day';
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION delete_account() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION delete_account() TO shelfkin_app;

-- +goose Down
DROP FUNCTION delete_account();
DROP TABLE deleted_accounts;

ALTER TABLE catalog_items
    DROP CONSTRAINT catalog_items_created_by_fkey,
    ADD CONSTRAINT catalog_items_created_by_fkey
        FOREIGN KEY (created_by) REFERENCES users (id);

ALTER TABLE household_invites
    DROP CONSTRAINT household_invites_created_by_fkey,
    ADD CONSTRAINT household_invites_created_by_fkey
        FOREIGN KEY (created_by) REFERENCES users (id);

ALTER TABLE households
    DROP CONSTRAINT households_created_by_fkey,
    ADD CONSTRAINT households_created_by_fkey
        FOREIGN KEY (created_by) REFERENCES users (id);
