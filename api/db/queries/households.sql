-- Every query here runs with dbtx.WithUser, so row-level security also
-- limits households and household_members to the caller's households.

-- name: CreateHousehold :one
-- Inserts the household and the caller's owner row (see the
-- create_household migration) and returns the new ID.
SELECT public.create_household(@name::text)::uuid AS id;

-- name: ListHouseholds :many
-- The caller's households with the caller's role and member count, sorted by
-- name case-insensitively (the ID breaks ties between duplicate names). The
-- count is a subquery in this one statement, so listing never runs a query per
-- household; it uses the household_members primary key (household_id first).
-- RLS lets the caller see every member of their own households.
SELECT h.id, h.name, m.role, h.created_at,
    (SELECT count(*) FROM household_members c WHERE c.household_id = h.id)::int AS member_count
FROM households h
JOIN household_members m ON m.household_id = h.id
WHERE m.user_id = @user_id
ORDER BY lower(h.name), h.id;

-- name: GetHousehold :one
-- One household with the caller's role and member count. No row if it does
-- not exist or the caller is not a member.
SELECT h.id, h.name, m.role, h.created_at,
    (SELECT count(*) FROM household_members c WHERE c.household_id = h.id)::int AS member_count
FROM households h
JOIN household_members m ON m.household_id = h.id
WHERE h.id = @id AND m.user_id = @user_id;

-- name: RenameHousehold :one
-- Owner only (checked by the API and by the households_update policy).
UPDATE households
SET name = @name, updated_at = now()
WHERE id = @id
RETURNING id, name, created_at,
    (SELECT count(*) FROM household_members c WHERE c.household_id = households.id)::int AS member_count;

-- name: DeleteHousehold :execrows
-- Owner only (checked by the API and by the households_delete policy). The
-- foreign keys cascade to members, invites, custom catalog items, item
-- defaults, pantry entries and item events.
DELETE FROM households WHERE id = @id;
