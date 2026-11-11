-- Household members (#15). Every query here runs with dbtx.WithUser, so
-- row-level security also limits household_members to the caller's
-- households.

-- name: ListMembers :many
-- The household's members with their profiles: the owner first, then the
-- members by joined_at (the user ID breaks ties).
SELECT m.user_id, u.display_name, u.email, m.role, m.joined_at
FROM household_members m
JOIN users u ON u.id = m.user_id
WHERE m.household_id = @household_id
ORDER BY (m.role = 'owner') DESC, m.joined_at, m.user_id;

-- name: DeleteMember :execrows
-- Removes a member, or the caller leaving. Only rows with role member are
-- deleted: the owner is never removed this way (they transfer ownership or
-- delete the household). The role filter is re-checked on a row that a
-- concurrent transfer_ownership changed while this waited, so a member who
-- has just become the owner is not removed. The household_members_delete
-- policy (#10) is a second check behind the API's.
DELETE FROM household_members
WHERE household_id = @household_id AND user_id = @user_id AND role = 'member';

-- name: TransferOwnership :exec
-- Demotes the caller (app.user_id) and promotes new_owner_id; see the
-- transfer_ownership migration for the checks and errors.
SELECT public.transfer_ownership(@household_id::uuid, @new_owner_id::uuid);
