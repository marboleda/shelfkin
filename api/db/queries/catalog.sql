-- Every query here runs with dbtx.WithUser.

-- name: ListCategories :many
-- The built-in categories in display order (the ID breaks ties).
SELECT id, key, name
FROM categories
ORDER BY sort_order, id;

-- name: ListLocations :many
-- The built-in storage locations in display order (the ID breaks ties).
SELECT id, key, name
FROM locations
ORDER BY sort_order, id;

-- name: SearchCatalog :many
-- Type-ahead search for one household, best match first: the built-in items
-- plus that household's custom items, with its effective defaults. See the
-- search_catalog migration for the matching, ranking and why it is a
-- SECURITY DEFINER function. Fails with SQLSTATE P0002 (no_data_found) if
-- the caller is not a member of the household.
-- A plpgsql function returns its rows from a tuplestore in the order RETURN
-- QUERY produced them, and a bare function scan keeps that order. (sqlc
-- cannot check ORDER BY ordinality, so WITH ORDINALITY is not used.)
SELECT s.catalog_item_id, s.name, s.is_custom, s.category_id, s.location_id, s.shelf_life_days
FROM public.search_catalog(@household_id::uuid, @q::text, @max_results::integer) s;
