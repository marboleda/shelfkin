-- The ingredient catalog (built-in and household custom items), each
-- household's own defaults for built-in items, and the view that combines
-- them. See docs/data-model.md (catalog_items, household_item_defaults).
--
-- catalog_items.created_by is nullable with no ON DELETE action, like
-- households.created_by. The rules that an override must be for a built-in
-- item, that a custom name must not match a built-in one, and keeping
-- updated_at current are the API's job (#18); there are no triggers.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE catalog_items (
    id                  uuid        PRIMARY KEY DEFAULT uuidv7(),
    -- NULL = built-in item; set = custom item, private to that household.
    household_id        uuid        REFERENCES households (id) ON DELETE CASCADE,
    name                text        NOT NULL,
    default_category_id smallint    NOT NULL REFERENCES categories (id),
    default_location_id smallint    NOT NULL REFERENCES locations (id),
    -- Typical shelf life, used to suggest an expiry date. NULL = no suggestion.
    shelf_life_days     integer,
    created_by          uuid        REFERENCES users (id),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

-- Names are unique per household, ignoring case. NULLS NOT DISTINCT makes
-- built-in names (household_id NULL) unique among themselves too.
CREATE UNIQUE INDEX catalog_items_household_id_lower_name_key
    ON catalog_items (household_id, lower(name)) NULLS NOT DISTINCT;

-- Type-ahead search on the name.
CREATE INDEX catalog_items_lower_name_trgm
    ON catalog_items USING gin (lower(name) gin_trgm_ops);

CREATE TABLE household_item_defaults (
    household_id    uuid        NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    -- Must be a built-in item (checked by the API).
    catalog_item_id uuid        NOT NULL REFERENCES catalog_items (id) ON DELETE CASCADE,
    -- NULL = use the catalog item's default.
    category_id     smallint    REFERENCES categories (id),
    location_id     smallint    REFERENCES locations (id),
    updated_by      uuid        REFERENCES users (id) ON DELETE SET NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (household_id, catalog_item_id)
);

-- One row per (household, item visible to that household): every built-in
-- item for every household, plus each household's own custom items, with the
-- household's override applied over the catalog default.
--
-- security_invoker makes the row-level security on the underlying tables (#10)
-- apply to whoever queries the view; without it the view would run with its
-- owner's rights and bypass RLS.
--
-- Filtering on household_id restricts households (and, through the join
-- conditions, household_item_defaults) to that household before the joins.
--
-- The ::boolean casts are no-ops for Postgres; they let sqlc type those
-- columns as bool instead of interface{}.
CREATE VIEW effective_catalog_items WITH (security_invoker = true) AS
SELECT h.id                                           AS household_id,
       c.id                                           AS catalog_item_id,
       c.name,
       (c.household_id IS NOT NULL)::boolean          AS is_custom,
       COALESCE(d.category_id, c.default_category_id) AS category_id,
       COALESCE(d.location_id, c.default_location_id) AS location_id,
       c.shelf_life_days,
       (d.category_id IS NOT NULL)::boolean           AS category_overridden,
       (d.location_id IS NOT NULL)::boolean           AS location_overridden
FROM households h
JOIN catalog_items c
  ON c.household_id IS NULL OR c.household_id = h.id
LEFT JOIN household_item_defaults d
  ON d.household_id = h.id AND d.catalog_item_id = c.id;

-- +goose Down
DROP VIEW effective_catalog_items;
DROP TABLE household_item_defaults;
DROP TABLE catalog_items;
DROP EXTENSION IF EXISTS pg_trgm;
