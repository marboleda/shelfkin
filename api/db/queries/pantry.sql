-- The pantry read (#20). Every query here runs with dbtx.WithUser, so
-- row-level security also limits entries, events and catalog items to the
-- caller's households.

-- name: CountPantryFilters :one
-- How many of the given IDs are locations and categories. The API passes each
-- list without duplicates, so a count below the list's length means a value
-- that is not an ID.
SELECT (SELECT count(*) FROM locations WHERE id = ANY(@location_ids::smallint[]))::int   AS locations,
       (SELECT count(*) FROM categories WHERE id = ANY(@category_ids::smallint[]))::int AS categories;

-- name: ListPantry :many
-- Every entry of the household with its catalog item as the household sees
-- it (effective_catalog_items: the household's category override first) and
-- its most recent item event, with the actor's current display name.
--
-- One query whatever the number of entries. The latest event is a LATERAL
-- subquery that reads one row from item_events_entry_id_occurred_at_idx per
-- entry; the event's columns then come from a primary key lookup (joined
-- separately so sqlc types them as nullable). users has no row-level
-- security, so the name of an actor who has left the household is still
-- found; a deleted actor's actor_id is NULL and so is the name.
--
-- An empty location_ids or category_ids array means no filter on it. The rows
-- are ordered so that all entries of an item are consecutive: items by name
-- (case-insensitive, then exact, then ID so the order is stable), entries by
-- purchase date, then creation time.
SELECT e.id, e.catalog_item_id, e.location_id, e.level,
       e.purchased_on, e.expires_on, e.created_at, e.updated_at,
       c.name::text             AS name,
       c.is_custom,
       c.category_id::smallint  AS category_id,
       ev.type                  AS event_type,
       ev.changes               AS event_changes,
       ev.actor_id              AS event_actor_id,
       u.display_name           AS event_actor_name,
       ev.occurred_at           AS event_occurred_at
FROM pantry_entries e
JOIN effective_catalog_items c
  ON c.household_id = e.household_id AND c.catalog_item_id = e.catalog_item_id
LEFT JOIN LATERAL (
    SELECT i.id
    FROM item_events i
    WHERE i.entry_id = e.id
    ORDER BY i.occurred_at DESC
    LIMIT 1
) latest ON true
LEFT JOIN item_events ev ON ev.id = latest.id
LEFT JOIN users u ON u.id = ev.actor_id
WHERE e.household_id = @household_id
  AND (cardinality(@location_ids::smallint[]) = 0 OR e.location_id = ANY(@location_ids::smallint[]))
  AND (cardinality(@category_ids::smallint[]) = 0 OR c.category_id = ANY(@category_ids::smallint[]))
ORDER BY lower(c.name), c.name, c.catalog_item_id, e.purchased_on, e.created_at, e.id;
