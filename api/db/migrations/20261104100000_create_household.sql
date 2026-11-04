-- create_household: the only way the API creates a household.
-- See docs/data-model.md (Access control) and #14.
--
-- shelfkin_app has no INSERT grant on households or household_members (#10),
-- so this SECURITY DEFINER function, owned by the migration user, inserts the
-- household and the caller's owner row in one call. The caller is read from
-- app.user_id, which api/internal/dbtx sets for the transaction; it never
-- comes from an argument, so a caller can only make itself the owner. Both
-- inserts happen in the caller's transaction: if the owner row fails, the
-- household row is rolled back with it.
--
-- The name is validated by the API (trimmed, 1 to 50 characters).
-- The empty search_path (with every name schema-qualified) stops a caller
-- from shadowing the tables used here.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION create_household(name text) RETURNS uuid
LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
    caller uuid := NULLIF(pg_catalog.current_setting('app.user_id', true), '')::uuid;
    household uuid;
BEGIN
    IF caller IS NULL THEN
        RAISE EXCEPTION 'create_household: app.user_id is not set'
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    INSERT INTO public.households (name, created_by)
    VALUES (create_household.name, caller)
    RETURNING id INTO household;

    INSERT INTO public.household_members (household_id, user_id, role)
    VALUES (household, caller, 'owner');

    RETURN household;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION create_household(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION create_household(text) TO shelfkin_app;

-- +goose Down
DROP FUNCTION create_household(text);
