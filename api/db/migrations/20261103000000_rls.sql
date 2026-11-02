-- Row-level security and the restricted role the API runs as.
-- See docs/data-model.md (Access control).
--
-- The API opens every transaction through api/internal/dbtx, which runs
-- SET LOCAL ROLE shelfkin_app and sets app.user_id for the transaction. The
-- policies below read the caller from that setting, so a query that forgets
-- WHERE household_id = ... still only sees the caller's households.
--
-- shelfkin_app is a server-wide role, so it is created only if it is missing
-- and is NOT dropped by the Down section: other databases on the same server
-- (parallel test databases, other environments) may still use it. Everything
-- else here (grants, policies, helper functions) is per database and is
-- removed by Down.
--
-- users, push_tokens and expiry_notifications have no RLS: they hold no
-- household data and the API scopes them by the caller's user ID.
-- categories and locations are read-only lookup tables.
--
-- shelfkin_app cannot INSERT into households or household_members, or UPDATE
-- household_members. Creating a household (#14), transferring ownership (#15)
-- and joining by invite code (#16) go through privileged functions instead.

-- +goose Up

-- Roles are server-wide, so several test databases migrated in parallel race
-- to create this one. A concurrent CREATE ROLE fails with duplicate_object
-- (the other one committed first) or unique_violation (both inserted into
-- pg_authid at once); either means the role now exists.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'shelfkin_app') THEN
        CREATE ROLE shelfkin_app NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
EXCEPTION
    WHEN duplicate_object OR unique_violation THEN
        NULL;
END
$$;
-- +goose StatementEnd

-- Grants. Anything not listed (including goose's version table) gets nothing.
GRANT USAGE ON SCHEMA public TO shelfkin_app;

GRANT SELECT ON categories, locations TO shelfkin_app;
GRANT SELECT, INSERT, UPDATE ON users TO shelfkin_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON push_tokens TO shelfkin_app;
GRANT SELECT, UPDATE, DELETE ON households TO shelfkin_app;
GRANT SELECT, DELETE ON household_members TO shelfkin_app;
GRANT SELECT, INSERT, UPDATE, DELETE
    ON household_invites, catalog_items, household_item_defaults, pantry_entries
    TO shelfkin_app;
GRANT SELECT, INSERT ON item_events TO shelfkin_app;
GRANT SELECT, INSERT, DELETE ON expiry_notifications TO shelfkin_app;
GRANT SELECT ON effective_catalog_items TO shelfkin_app;

-- The households the current user (app.user_id) belongs to. NULLIF turns the
-- '' that a pooled connection reports after an earlier transaction set the
-- value into NULL, so an unset user matches nothing instead of failing the
-- ::uuid cast.
--
-- SECURITY DEFINER: it runs as the migration user, which owns
-- household_members and so is not subject to its policies. A policy on
-- household_members that queried household_members directly would fail with
-- "infinite recursion detected in policy". The empty search_path (with every
-- name schema-qualified) stops a caller from shadowing household_members.
-- +goose StatementBegin
CREATE FUNCTION app_user_household_ids() RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = ''
AS $$
    SELECT household_id
    FROM public.household_members
    WHERE user_id = NULLIF(pg_catalog.current_setting('app.user_id', true), '')::uuid
$$;
-- +goose StatementEnd

-- The subset of app_user_household_ids() where the current user is the owner,
-- for the owner-only policies (a second check behind the API).
-- +goose StatementBegin
CREATE FUNCTION app_user_owned_household_ids() RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = ''
AS $$
    SELECT household_id
    FROM public.household_members
    WHERE user_id = NULLIF(pg_catalog.current_setting('app.user_id', true), '')::uuid
      AND role = 'owner'
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION app_user_household_ids(), app_user_owned_household_ids() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app_user_household_ids(), app_user_owned_household_ids() TO shelfkin_app;

-- households: scoped by its own id. Members read; only the owner renames or
-- deletes. There is no INSERT grant (create_household, #14).
ALTER TABLE households ENABLE ROW LEVEL SECURITY;

CREATE POLICY households_select ON households FOR SELECT TO shelfkin_app
    USING (id IN (SELECT app_user_household_ids()));
CREATE POLICY households_update ON households FOR UPDATE TO shelfkin_app
    USING (id IN (SELECT app_user_owned_household_ids()))
    WITH CHECK (id IN (SELECT app_user_owned_household_ids()));
CREATE POLICY households_delete ON households FOR DELETE TO shelfkin_app
    USING (id IN (SELECT app_user_owned_household_ids()));

-- household_members: members read; there is no INSERT or UPDATE grant
-- (#14, #15, #16). The owner may remove another member, and any member may
-- leave, except the owner (ownership must be transferred first).
ALTER TABLE household_members ENABLE ROW LEVEL SECURITY;

CREATE POLICY household_members_select ON household_members FOR SELECT TO shelfkin_app
    USING (household_id IN (SELECT app_user_household_ids()));
CREATE POLICY household_members_delete ON household_members FOR DELETE TO shelfkin_app
    USING (
        (household_id IN (SELECT app_user_owned_household_ids())
         AND user_id IS DISTINCT FROM NULLIF(current_setting('app.user_id', true), '')::uuid)
        OR
        (household_id IN (SELECT app_user_household_ids())
         AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
         AND role <> 'owner')
    );

-- household_invites: owner only, for every operation.
ALTER TABLE household_invites ENABLE ROW LEVEL SECURITY;

CREATE POLICY household_invites_owner ON household_invites FOR ALL TO shelfkin_app
    USING (household_id IN (SELECT app_user_owned_household_ids()))
    WITH CHECK (household_id IN (SELECT app_user_owned_household_ids()));

-- catalog_items: built-ins (household_id IS NULL) are readable by everyone
-- and writable by no one through this role; custom items belong to their
-- household. NULL IN (...) is never true, so the write policies reject
-- built-ins, including turning a custom item into one.
ALTER TABLE catalog_items ENABLE ROW LEVEL SECURITY;

CREATE POLICY catalog_items_select ON catalog_items FOR SELECT TO shelfkin_app
    USING (household_id IS NULL OR household_id IN (SELECT app_user_household_ids()));
CREATE POLICY catalog_items_insert ON catalog_items FOR INSERT TO shelfkin_app
    WITH CHECK (household_id IN (SELECT app_user_household_ids()));
CREATE POLICY catalog_items_update ON catalog_items FOR UPDATE TO shelfkin_app
    USING (household_id IN (SELECT app_user_household_ids()))
    WITH CHECK (household_id IN (SELECT app_user_household_ids()));
CREATE POLICY catalog_items_delete ON catalog_items FOR DELETE TO shelfkin_app
    USING (household_id IN (SELECT app_user_household_ids()));

-- household_item_defaults and pantry_entries: any member, every operation.
ALTER TABLE household_item_defaults ENABLE ROW LEVEL SECURITY;

CREATE POLICY household_item_defaults_member ON household_item_defaults FOR ALL TO shelfkin_app
    USING (household_id IN (SELECT app_user_household_ids()))
    WITH CHECK (household_id IN (SELECT app_user_household_ids()));

ALTER TABLE pantry_entries ENABLE ROW LEVEL SECURITY;

CREATE POLICY pantry_entries_member ON pantry_entries FOR ALL TO shelfkin_app
    USING (household_id IN (SELECT app_user_household_ids()))
    WITH CHECK (household_id IN (SELECT app_user_household_ids()));

-- item_events: append-only. The grants allow only SELECT and INSERT.
ALTER TABLE item_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY item_events_select ON item_events FOR SELECT TO shelfkin_app
    USING (household_id IN (SELECT app_user_household_ids()));
CREATE POLICY item_events_insert ON item_events FOR INSERT TO shelfkin_app
    WITH CHECK (household_id IN (SELECT app_user_household_ids()));

-- +goose Down
DROP POLICY item_events_insert ON item_events;
DROP POLICY item_events_select ON item_events;
ALTER TABLE item_events DISABLE ROW LEVEL SECURITY;

DROP POLICY pantry_entries_member ON pantry_entries;
ALTER TABLE pantry_entries DISABLE ROW LEVEL SECURITY;

DROP POLICY household_item_defaults_member ON household_item_defaults;
ALTER TABLE household_item_defaults DISABLE ROW LEVEL SECURITY;

DROP POLICY catalog_items_delete ON catalog_items;
DROP POLICY catalog_items_update ON catalog_items;
DROP POLICY catalog_items_insert ON catalog_items;
DROP POLICY catalog_items_select ON catalog_items;
ALTER TABLE catalog_items DISABLE ROW LEVEL SECURITY;

DROP POLICY household_invites_owner ON household_invites;
ALTER TABLE household_invites DISABLE ROW LEVEL SECURITY;

DROP POLICY household_members_delete ON household_members;
DROP POLICY household_members_select ON household_members;
ALTER TABLE household_members DISABLE ROW LEVEL SECURITY;

DROP POLICY households_delete ON households;
DROP POLICY households_update ON households;
DROP POLICY households_select ON households;
ALTER TABLE households DISABLE ROW LEVEL SECURITY;

DROP FUNCTION app_user_owned_household_ids();
DROP FUNCTION app_user_household_ids();

REVOKE ALL ON
    categories, locations, users, push_tokens, households, household_members,
    household_invites, catalog_items, household_item_defaults, pantry_entries,
    item_events, expiry_notifications, effective_catalog_items
    FROM shelfkin_app;
REVOKE USAGE ON SCHEMA public FROM shelfkin_app;

-- The shelfkin_app role is deliberately kept; see the note at the top.
