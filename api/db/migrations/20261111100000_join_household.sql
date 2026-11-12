-- join_household: the only way a user adds themselves to a household.
-- See docs/data-model.md (household_invites, Access control) and #16.
--
-- The caller is not a member yet, so as shelfkin_app they can neither read
-- the invite (the household_invites_owner policy is owner only) nor insert a
-- household_members row (no INSERT grant, #10). This SECURITY DEFINER
-- function, owned by the migration user, does both for one invite code, in
-- the caller's transaction, and tells the caller only the outcome: a status
-- and, when they are (now) a member, the household ID. It never returns
-- anything else about the invite or the household.
--
-- The caller is read from app.user_id (set by api/internal/dbtx), never from
-- an argument. The function only ever inserts a 'member' row; it never
-- updates or deletes one. The code is matched exactly: the API normalises it
-- (removes whitespace and dashes, upper-cases) before calling.
--
-- Result (one row), which the API maps to its responses:
--   status          household_id
--   joined          the household   the caller was added as a member
--   already_member  the household   the caller was already a member (any
--                                   role); checked before revoked and
--                                   expired, so an old link still works for
--                                   a member
--   not_found       NULL            no invite has this code (or its household
--                                   was deleted while the join waited)
--   revoked         NULL            the invite was revoked (checked before
--                                   expired)
--   expired         NULL            expires_at <= now()
--
-- Errors (SQLSTATE):
--   42501 insufficient_privilege  app.user_id is not set
--
-- Concurrency: the household row is locked (FOR KEY SHARE) and then the
-- invite row (FOR SHARE), in the same order as DELETE FROM households and
-- its cascade to household_invites, so a concurrent household deletion is
-- ordered before or after the join and never deadlocks with it. A deletion
-- that commits while the join waits leaves no row to lock: not_found. A
-- concurrent revoke (an UPDATE of the invite) likewise runs entirely before
-- or after the join. Two joins by the same user race on the primary key: the
-- second waits, then ON CONFLICT DO NOTHING inserts nothing and it reports
-- already_member.
--
-- The OUT parameters household_id and status share names with columns, so
-- the body prefers columns (#variable_conflict use_column) and qualifies
-- every column anyway. The empty search_path (with every name
-- schema-qualified) stops a caller from shadowing the tables used here.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION join_household(code text, OUT household_id uuid, OUT status text)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = ''
AS $$
#variable_conflict use_column
DECLARE
    caller uuid := NULLIF(pg_catalog.current_setting('app.user_id', true), '')::uuid;
    target uuid;
    invite_expires_at timestamptz;
    invite_revoked_at timestamptz;
    inserted bigint;
BEGIN
    IF caller IS NULL THEN
        RAISE EXCEPTION 'join_household: app.user_id is not set'
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    -- Find the household first, without a lock, so it can be locked before
    -- the invite (see Concurrency above).
    SELECT i.household_id INTO target
    FROM public.household_invites i
    WHERE i.code = join_household.code;
    IF NOT FOUND THEN
        household_id := NULL;
        status := 'not_found';
        RETURN;
    END IF;

    PERFORM 1 FROM public.households h
    WHERE h.id = target
    FOR KEY SHARE;
    IF NOT FOUND THEN
        household_id := NULL;
        status := 'not_found';
        RETURN;
    END IF;

    -- Read the invite again, locked, for its latest revoked_at.
    SELECT i.expires_at, i.revoked_at INTO invite_expires_at, invite_revoked_at
    FROM public.household_invites i
    WHERE i.code = join_household.code AND i.household_id = target
    FOR SHARE;
    IF NOT FOUND THEN
        household_id := NULL;
        status := 'not_found';
        RETURN;
    END IF;

    PERFORM 1 FROM public.household_members m
    WHERE m.household_id = target AND m.user_id = caller;
    IF FOUND THEN
        household_id := target;
        status := 'already_member';
        RETURN;
    END IF;

    IF invite_revoked_at IS NOT NULL THEN
        household_id := NULL;
        status := 'revoked';
        RETURN;
    END IF;

    IF invite_expires_at <= pg_catalog.now() THEN
        household_id := NULL;
        status := 'expired';
        RETURN;
    END IF;

    INSERT INTO public.household_members (household_id, user_id, role)
    VALUES (target, caller, 'member')
    ON CONFLICT (household_id, user_id) DO NOTHING;
    GET DIAGNOSTICS inserted = ROW_COUNT;

    household_id := target;
    IF inserted = 0 THEN
        status := 'already_member';
    ELSE
        status := 'joined';
    END IF;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION join_household(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION join_household(text) TO shelfkin_app;

-- +goose Down
DROP FUNCTION join_household(text);
