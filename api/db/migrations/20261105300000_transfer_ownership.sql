-- transfer_ownership: the only way the API changes a member's role.
-- See docs/data-model.md (household_members, Access control) and #15.
--
-- shelfkin_app has no UPDATE grant on household_members (#10). Even with one,
-- the owner could not do this as shelfkin_app: after demoting themselves they
-- are no longer the owner, so an owner-only policy would block the promote.
-- This SECURITY DEFINER function, owned by the migration user, does both
-- updates in the caller's transaction.
--
-- The caller is read from app.user_id (set by api/internal/dbtx), never from
-- an argument. The function fails, and changes nothing, unless the caller is
-- the household's owner and new_owner_id is another member of it.
--
-- Errors (SQLSTATE), which the API maps to its error codes:
--   42501 insufficient_privilege  app.user_id is not set, or the caller is
--                                 not the owner (or not a member, or the
--                                 household does not exist)
--   22023 invalid_parameter_value new_owner_id is the caller (already owner)
--   P0002 no_data_found           new_owner_id is not a member
--
-- Concurrency: the household row is locked (FOR UPDATE) before anything is
-- checked, so transfers of one household run one at a time. A second
-- transfer waits for the first to commit, then sees the new owner and fails
-- with 42501 because its caller is no longer the owner.
--
-- The demote runs before the promote: the partial unique index
-- household_members_one_owner is checked per statement, so promoting first
-- would briefly make two owners and fail.
--
-- The empty search_path (with every name schema-qualified) stops a caller
-- from shadowing the tables used here.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION transfer_ownership(household_id uuid, new_owner_id uuid) RETURNS void
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
    caller uuid := NULLIF(pg_catalog.current_setting('app.user_id', true), '')::uuid;
BEGIN
    IF caller IS NULL THEN
        RAISE EXCEPTION 'transfer_ownership: app.user_id is not set'
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    PERFORM 1 FROM public.households h
    WHERE h.id = transfer_ownership.household_id
    FOR UPDATE;

    PERFORM 1 FROM public.household_members m
    WHERE m.household_id = transfer_ownership.household_id
      AND m.user_id = caller
      AND m.role = 'owner';
    IF NOT FOUND THEN
        RAISE EXCEPTION 'transfer_ownership: the caller is not the owner of this household'
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    IF new_owner_id = caller THEN
        RAISE EXCEPTION 'transfer_ownership: the caller is already the owner'
            USING ERRCODE = 'invalid_parameter_value';
    END IF;

    PERFORM 1 FROM public.household_members m
    WHERE m.household_id = transfer_ownership.household_id
      AND m.user_id = new_owner_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'transfer_ownership: the new owner is not a member of this household'
            USING ERRCODE = 'no_data_found';
    END IF;

    UPDATE public.household_members m
    SET role = 'member'
    WHERE m.household_id = transfer_ownership.household_id
      AND m.user_id = caller;

    -- The target's row may have been deleted (removed or left) after the
    -- check above; then nothing is promoted and the whole call fails, so the
    -- household is never left without an owner.
    UPDATE public.household_members m
    SET role = 'owner'
    WHERE m.household_id = transfer_ownership.household_id
      AND m.user_id = new_owner_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'transfer_ownership: the new owner is not a member of this household'
            USING ERRCODE = 'no_data_found';
    END IF;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION transfer_ownership(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION transfer_ownership(uuid, uuid) TO shelfkin_app;

-- +goose Down
DROP FUNCTION transfer_ownership(uuid, uuid);
