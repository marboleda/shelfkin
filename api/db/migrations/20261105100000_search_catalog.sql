-- search_catalog: type-ahead search over the catalog for one household (#17).
-- See docs/data-model.md (catalog_items, Access control).
--
-- Why a SECURITY DEFINER function: under row-level security Postgres only
-- uses an index for a condition whose operator is leakproof, so that a
-- policy cannot be bypassed by an operator that leaks the rows it sees. LIKE
-- and the pg_trgm operators are not leakproof, so as shelfkin_app the
-- search could never use catalog_items_lower_name_trgm. This function runs
-- as its owner (the migration user, which owns catalog_items and so is not
-- subject to its policies) and does the scoping itself, the same way the
-- policies would:
--
-- - The caller is read from app.user_id (set by api/internal/dbtx), never
--   from an argument. If household_id is not one of the caller's households
--   (app_user_household_ids()), it raises no_data_found (P0002), which the
--   API turns into 404. A household that does not exist looks the same.
-- - It reads effective_catalog_items for that household only: the built-in
--   items plus that household's custom items, with that household's
--   overrides applied. Other households' custom items never match.
--
-- The API validates q (trimmed, at most 100 characters) and max_results
-- (1 to 25); an empty q returns no rows after the membership check.
--
-- Matching is case-insensitive, and %, _ and \ in q match literally. Ranking,
-- best first (match_rank, which orders the rows but is not returned):
--   1. exact name
--   2. name starts with q
--   3. a word in the name starts with q (after a non-alphanumeric character)
--   4. fuzzy: q <% lower(name), trigram word similarity at or above
--      pg_trgm.word_similarity_threshold (default 0.6). Only for q of 3 or
--      more characters; shorter q have too few trigrams to be meaningful.
-- Within rank 4 a higher similarity comes first. Remaining ties go to the
-- shorter name, then alphabetically (case-insensitive, then exact, then id
-- so the order is stable).
--
-- Plan: the candidates come from catalog_items through the trigram GIN index
-- (catalog_items_lower_name_trgm): the LIKE '%q%' and <% conditions are the
-- ones it answers, as a BitmapOr; the rank conditions are rechecked on the
-- candidates. The household condition is written as
-- COALESCE(household_id, hid) = hid, which no index answers, and the CTE is
-- MATERIALIZED so the planner cannot merge it into the view's join; otherwise
-- may scan by household_id instead (#17 requires the trigram index). The effective
-- category and location then come from effective_catalog_items for this
-- household, joined on the candidates' IDs (primary key lookups).
-- With a few thousand rows the planner sometimes costs a sequential scan of
-- catalog_items as cheaper; enable_seqscan = off (for this function only)
-- leaves the trigram index as the only way to find the candidates, so the
-- plan does not change with table statistics.
-- plan_cache_mode = force_custom_plan makes plpgsql plan the
-- query with the actual q on every call instead of switching to a generic
-- plan, so the planner can see how selective the pattern is.
--
-- The empty search_path (with every name schema-qualified, including the
-- pg_trgm function and operator from public) stops a caller from shadowing
-- the objects used here.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION search_catalog(household_id uuid, q text, max_results integer)
RETURNS SETOF effective_catalog_items
LANGUAGE plpgsql STABLE SECURITY DEFINER
SET search_path = ''
SET plan_cache_mode = force_custom_plan
SET enable_seqscan = off
AS $$
#variable_conflict use_column
DECLARE
    hid      uuid    := search_catalog.household_id;
    lq       text    := pg_catalog.lower(search_catalog.q);
    -- q escaped for LIKE (the default escape character is \).
    pat      text;
    -- Start of a word followed by q, with q's punctuation escaped for the
    -- regular expression.
    word_re  text;
    fuzzy    boolean;
BEGIN
    IF NOT EXISTS (SELECT FROM public.app_user_household_ids() AS m(id) WHERE m.id = hid) THEN
        RAISE EXCEPTION 'search_catalog: household not found'
            USING ERRCODE = 'no_data_found';
    END IF;

    IF lq IS NULL OR lq = '' OR max_results IS NULL OR max_results < 1 THEN
        RETURN;
    END IF;

    pat := pg_catalog.replace(pg_catalog.replace(pg_catalog.replace(
               lq, '\', '\\'), '%', '\%'), '_', '\_');
    word_re := '(^|[^[:alnum:]])'
            || pg_catalog.regexp_replace(lq, '([^[:alnum:][:space:]])', '\\\1', 'g');
    fuzzy := pg_catalog.char_length(lq) >= 3;

    RETURN QUERY
    WITH candidates AS MATERIALIZED (
        -- The matching items this household can see, found through the
        -- trigram index (see "Plan" above).
        SELECT c.id,
               CASE
                   WHEN pg_catalog.lower(c.name) = lq THEN 1
                   WHEN pg_catalog.lower(c.name) LIKE pat || '%' THEN 2
                   WHEN pg_catalog.lower(c.name) ~ word_re THEN 3
                   ELSE 4
               END AS match_rank
        FROM public.catalog_items c
        WHERE COALESCE(c.household_id, hid) = hid
          AND (
                (pg_catalog.lower(c.name) LIKE '%' || pat || '%'
                 AND (pg_catalog.lower(c.name) LIKE pat || '%'
                      OR pg_catalog.lower(c.name) ~ word_re))
             OR (fuzzy AND lq OPERATOR(public.<%) pg_catalog.lower(c.name))
          )
    )
    SELECT m.household_id, m.catalog_item_id, m.name, m.is_custom, m.category_id,
           m.location_id, m.shelf_life_days, m.category_overridden, m.location_overridden
    FROM (
        SELECT e.*, k.match_rank
        FROM candidates k
        JOIN public.effective_catalog_items e
          ON e.catalog_item_id = k.id AND e.household_id = hid
    ) m
    ORDER BY m.match_rank,
             CASE WHEN m.match_rank = 4
                  THEN -public.word_similarity(lq, pg_catalog.lower(m.name)) END,
             pg_catalog.char_length(m.name),
             pg_catalog.lower(m.name),
             m.name,
             m.catalog_item_id
    LIMIT max_results;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION search_catalog(uuid, text, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION search_catalog(uuid, text, integer) TO shelfkin_app;

-- +goose Down
DROP FUNCTION search_catalog(uuid, text, integer);
