-- Keep pantry entries and item defaults from pointing at another household's
-- catalog items (#73). See docs/data-model.md (Access control).
--
-- Foreign-key checks run without row-level security, so with only the #10
-- policies a member of household A could insert an entry or a default in A
-- whose catalog_item_id is household B's custom item. The item stayed
-- invisible to A, but B could then no longer delete it (or household B), and
-- the 23503 error told A whether an item ID existed. The API checks the item
-- before writing; these WITH CHECK clauses are the second check behind it.
--
-- The subqueries on catalog_items run under catalog_items' own policies,
-- which is fine: built-ins and the caller's custom items are visible, and
-- the rule only ever accepts those. A missing item and another household's
-- item now both fail the same way, with 42501.
--
-- catalog_items has its own household_id column, so the outer row's columns
-- are qualified with the table name; an unqualified household_id would
-- compare the catalog item with itself.
--
-- Only WITH CHECK changes. The USING clauses (which rows can be read,
-- updated and deleted) stay as #10 left them.

-- +goose Up

-- An entry's item is a built-in or a custom item of the entry's own
-- household. "Any item the caller can see" would be too loose: a member of A
-- and C can see C's custom items but must not put one in A's pantry.
ALTER POLICY pantry_entries_member ON pantry_entries
    WITH CHECK (
        household_id IN (SELECT app_user_household_ids())
        AND EXISTS (
            SELECT FROM catalog_items c
            WHERE c.id = pantry_entries.catalog_item_id
              AND (c.household_id IS NULL OR c.household_id = pantry_entries.household_id)
        )
    );

-- Defaults are for built-in items only; custom items keep their defaults on
-- their own row.
ALTER POLICY household_item_defaults_member ON household_item_defaults
    WITH CHECK (
        household_id IN (SELECT app_user_household_ids())
        AND EXISTS (
            SELECT FROM catalog_items c
            WHERE c.id = household_item_defaults.catalog_item_id
              AND c.household_id IS NULL
        )
    );

-- +goose Down

-- Restore the #10 checks exactly.
ALTER POLICY household_item_defaults_member ON household_item_defaults
    WITH CHECK (household_id IN (SELECT app_user_household_ids()));

ALTER POLICY pantry_entries_member ON pantry_entries
    WITH CHECK (household_id IN (SELECT app_user_household_ids()));
