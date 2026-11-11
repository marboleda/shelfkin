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
       e.catalog_item_id, e.name, e.is_custom, e.category_id, e.location_id, e.shelf_life_days
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
