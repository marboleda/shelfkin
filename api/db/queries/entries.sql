-- Pantry entry writes and their item events (#19). Every query here runs with
-- dbtx.WithUser, so row-level security also limits entries, events and
-- catalog items to the caller's households.

-- name: GetEntryCatalogItem :one
-- The catalog item as this household sees it: built-in items and the
-- household's own custom items only (another household's custom item gives
-- no row, the same as a missing one), with the household's default location.
SELECT catalog_item_id::uuid    AS catalog_item_id,
       name::text               AS name,
       location_id::smallint    AS location_id,
       shelf_life_days
FROM effective_catalog_items
WHERE household_id = @household_id AND catalog_item_id = @catalog_item_id;

-- name: LocationExists :one
SELECT EXISTS (SELECT 1 FROM locations WHERE id = @id)::boolean AS exists;

-- name: InsertEntry :one
-- Returns no row when an entry with this id already exists, whether or not
-- the caller can see it.
INSERT INTO pantry_entries (
    id, household_id, catalog_item_id, location_id, level,
    purchased_on, expires_on, created_by, updated_by
) VALUES (
    @id, @household_id, @catalog_item_id, @location_id, @level,
    @purchased_on, @expires_on, @actor_id, @actor_id
)
ON CONFLICT (id) DO NOTHING
RETURNING id, household_id, catalog_item_id, location_id, level,
          purchased_on, expires_on, created_at, updated_at;

-- name: GetEntryByID :one
-- An entry in any household the caller can see. No row if it does not exist
-- or belongs to a household the caller is not in.
SELECT e.id, e.household_id, e.catalog_item_id, e.location_id, e.level,
       e.purchased_on, e.expires_on, e.created_at, e.updated_at,
       c.name
FROM pantry_entries e
JOIN catalog_items c ON c.id = e.catalog_item_id
WHERE e.id = @id;

-- name: LockEntry :one
-- An entry of this household, locked until the end of the transaction so
-- concurrent changes to it apply one after the other.
SELECT e.id, e.household_id, e.catalog_item_id, e.location_id, e.level,
       e.purchased_on, e.expires_on, e.created_at, e.updated_at,
       c.name
FROM pantry_entries e
JOIN catalog_items c ON c.id = e.catalog_item_id
WHERE e.id = @id AND e.household_id = @household_id
FOR UPDATE OF e;

-- name: UpdateEntry :one
-- Writes every editable column; the API has locked the row and changes one.
-- clock_timestamp(), not now() (the transaction's start): concurrent changes
-- wait for the row lock, so only the time of the write orders them the way
-- they were applied.
UPDATE pantry_entries
SET level        = @level,
    location_id  = @location_id,
    purchased_on = @purchased_on,
    expires_on   = @expires_on,
    updated_by   = @actor_id,
    updated_at   = clock_timestamp()
WHERE id = @id AND household_id = @household_id
RETURNING id, household_id, catalog_item_id, location_id, level,
          purchased_on, expires_on, created_at, updated_at;

-- name: DeleteEntry :execrows
DELETE FROM pantry_entries WHERE id = @id AND household_id = @household_id;

-- name: GetEvent :one
-- An event the caller can see. No row if it does not exist or belongs to a
-- household the caller is not in.
SELECT id, household_id, entry_id, catalog_item_id, type
FROM item_events
WHERE id = @id;

-- name: InsertEvent :execrows
-- 0 rows when an event with this id already exists, whether or not the
-- caller can see it. occurred_at is clock_timestamp() for the same reason as
-- in UpdateEntry: the latest event of an entry by occurred_at must be the
-- change that was applied last.
INSERT INTO item_events (
    id, household_id, entry_id, catalog_item_id, actor_id, type, changes, snapshot, occurred_at
) VALUES (
    @id, @household_id, @entry_id, @catalog_item_id, @actor_id, @type, @changes, @snapshot, clock_timestamp()
)
ON CONFLICT (id) DO NOTHING;
