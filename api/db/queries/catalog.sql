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
-- Type-ahead search for one household (#17), best match first: the built-in
-- items plus that household's custom items, with its effective defaults
-- (effective_catalog_items). Runs under row-level security like every other
-- read.
--
-- It starts from households, which RLS limits to the caller's households,
-- and LEFT JOINs the matching items of effective_catalog_items. So it returns:
-- - no row if the household does not exist or the caller is not a member
--   (the API answers 404);
-- - one row with a NULL catalog_item_id if the caller is a member and
--   nothing matches, or q is empty (the API answers 200 with no items);
-- - otherwise one row per match, best first.
--
-- The API trims q, rejects one over 100 characters, and checks max_results
-- is 1 to 25.
--
-- Matching is case-insensitive, and %, _ and \ in q match literally: pat is
-- q escaped for LIKE (whose default escape character is \), and word_re is
-- the start of a word followed by q, with every punctuation character in q
-- escaped for the regular expression. Ranking, best first:
--   1. exact name
--   2. name starts with q
--   3. a word in the name starts with q (after a non-alphanumeric character)
--   4. fuzzy: q <% lower(name), trigram word similarity at or above
--      pg_trgm.word_similarity_threshold (default 0.6). Only for a q of 3 or
--      more characters; shorter ones have too few trigrams to mean anything.
--      pg_trgm ignores punctuation, so only q's letters and digits count.
-- Within rank 4 a higher similarity comes first. Remaining ties go to the
-- shorter name, then alphabetically (case-insensitive, then exact, then the
-- ID so the order is stable).
--
-- Plan: the household conditions (households.id, and inside the view
-- catalog_items.household_id IS NULL or = the household) use leakproof
-- operators, so under RLS the planner can use them as index conditions. It
-- reads the built-in items plus this household's custom items, however many
-- items other households have, and checks the name only on those.
SELECT h.id AS household_id,
       e.catalog_item_id, e.name, e.is_custom, e.category_id, e.location_id, e.shelf_life_days,
       e.category_overridden, e.location_overridden
FROM households h
CROSS JOIN LATERAL (
    SELECT lower(@q::text) AS lq,
           replace(replace(replace(lower(@q::text), '\', '\\'), '%', '\%'), '_', '\_') AS pat,
           '(^|[^[:alnum:]])' || regexp_replace(lower(@q::text), '([^[:alnum:][:space:]])', '\\\1', 'g') AS word_re,
           char_length(@q::text) >= 3 AS fuzzy
) p
LEFT JOIN effective_catalog_items e
  ON e.household_id = h.id
 AND p.lq <> ''
 AND (
       (lower(e.name) LIKE '%' || p.pat || '%'
        AND (lower(e.name) LIKE p.pat || '%' OR lower(e.name) ~ p.word_re))
    OR (p.fuzzy AND p.lq <% lower(e.name))
 )
CROSS JOIN LATERAL (
    SELECT CASE
               WHEN lower(e.name) = p.lq THEN 1
               WHEN lower(e.name) LIKE p.pat || '%' THEN 2
               WHEN lower(e.name) ~ p.word_re THEN 3
               ELSE 4
           END AS match_rank
) r
WHERE h.id = @household_id::uuid
ORDER BY r.match_rank,
         CASE WHEN r.match_rank = 4 THEN -word_similarity(p.lq, lower(e.name)) END,
         char_length(e.name), lower(e.name), e.name, e.catalog_item_id
LIMIT @max_results::integer;

-- Custom items and household defaults (#18). The API checks the household
-- and the item before every write: row-level security (#10, #73) is the
-- second check behind it, not the way the API finds these cases.

-- name: CategoryExists :one
SELECT EXISTS (SELECT 1 FROM categories WHERE id = @id)::boolean AS exists;

-- name: GetCatalogItem :one
-- One item as this household sees it (effective_catalog_items), with the
-- item's own defaults. No row if the household does not exist, the caller is
-- not a member, or the item is neither built-in nor this household's custom
-- item.
SELECT e.catalog_item_id::uuid        AS catalog_item_id,
       e.name::text                   AS name,
       e.is_custom::boolean           AS is_custom,
       e.category_id::smallint        AS category_id,
       e.location_id::smallint        AS location_id,
       e.shelf_life_days,
       e.category_overridden::boolean AS category_overridden,
       e.location_overridden::boolean AS location_overridden,
       c.default_category_id,
       c.default_location_id
FROM effective_catalog_items e
JOIN catalog_items c ON c.id = e.catalog_item_id
WHERE e.household_id = @household_id AND e.catalog_item_id = @catalog_item_id;

-- name: CatalogNameTaken :one
-- Whether name matches a built-in item or one of this household's custom
-- items, ignoring case. The unique index on (household_id, lower(name))
-- catches a custom name created concurrently; this catches the built-ins,
-- which the index does not cover.
SELECT EXISTS (
    SELECT 1 FROM catalog_items
    WHERE (household_id IS NULL OR household_id = @household_id)
      AND lower(name) = lower(@name::text)
)::boolean AS taken;

-- name: InsertCustomItem :one
INSERT INTO catalog_items (
    household_id, name, default_category_id, default_location_id, shelf_life_days, created_by
) VALUES (
    @household_id, @name, @category_id, @location_id, sqlc.narg('shelf_life_days')::integer, @created_by
)
RETURNING id;

-- name: UpdateCustomItem :execrows
-- A NULL category_id or location_id leaves that column as it is;
-- shelf_life_days is written only when set_shelf_life is true (NULL clears
-- it).
UPDATE catalog_items
SET default_category_id = COALESCE(sqlc.narg('category_id')::smallint, default_category_id),
    default_location_id = COALESCE(sqlc.narg('location_id')::smallint, default_location_id),
    shelf_life_days     = CASE WHEN @set_shelf_life::boolean
                               THEN sqlc.narg('shelf_life_days')::integer
                               ELSE shelf_life_days END,
    updated_at          = now()
WHERE id = @id AND household_id = @household_id;

-- name: UpsertItemDefaults :exec
-- Sets this household's override of a built-in item. Only the columns whose
-- set_* flag is true change; NULL means "use the built-in default". The API
-- passes NULL for a value equal to the built-in default. One statement, so
-- two members changing different columns at the same time both win.
INSERT INTO household_item_defaults (household_id, catalog_item_id, category_id, location_id, updated_by)
VALUES (@household_id, @catalog_item_id, sqlc.narg('category_id')::smallint, sqlc.narg('location_id')::smallint, @updated_by)
ON CONFLICT (household_id, catalog_item_id) DO UPDATE
SET category_id = CASE WHEN @set_category::boolean
                       THEN EXCLUDED.category_id
                       ELSE household_item_defaults.category_id END,
    location_id = CASE WHEN @set_location::boolean
                       THEN EXCLUDED.location_id
                       ELSE household_item_defaults.location_id END,
    updated_by  = EXCLUDED.updated_by,
    updated_at  = now();

-- name: DeleteEmptyItemDefaults :exec
-- Removes the override row once neither column overrides anything.
DELETE FROM household_item_defaults
WHERE household_id = @household_id
  AND catalog_item_id = @catalog_item_id
  AND category_id IS NULL
  AND location_id IS NULL;
