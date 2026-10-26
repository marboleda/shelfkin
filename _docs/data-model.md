# Shelfkin — Data Model (v1 draft)

*Draft — October 26, 2026*

Postgres (Cloud SQL). Every table that holds household data has a `household_id` column, so a single RLS rule can scope it (see [Access control](#access-control)).

## Conventions

- **IDs:** UUIDv7 (time-ordered). The client generates them for `pantry_entries` and `item_events`, so full offline editing later needs no schema change; the server generates all others.
- **Timestamps:** `timestamptz`, stored in UTC. Calendar dates (expiry, purchase) are `date`.
- **Enums:** Postgres enum types for small fixed sets (`member_role`, `stock_level`, `event_type`).
- **Deletes:** Hard deletes. History survives because `item_events` keeps the IDs and a snapshot, not foreign keys to deleted rows.

## Overview

```
users ─┬─< household_members >─── households ─┬─< household_invites
       │                                       ├─< catalog_items (custom) ──< pantry_entries
       │                                       ├─< household_item_defaults >── catalog_items (built-in)
       ├─< push_tokens                         ├─< pantry_entries
       │                                       └─< item_events
       └─ (actor on entries / events)

catalog_items (built-in, household_id NULL) ──< pantry_entries
categories, locations (built-in lookup tables)
```

## Tables

### `users`

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | Internal ID, used in all FKs |
| `firebase_uid` | text, unique, not null | Maps Firebase Auth identity to our user |
| `display_name` | text, not null | Shown in "Set to Low by Sam" |
| `email` | text | Copied from Firebase for display/invites |
| `timezone` | text, not null | IANA name, e.g. `America/Toronto`; used to send expiry pushes at a sensible local time |
| `push_enabled` | boolean, not null, default false | Push notifications are opt-in |
| `created_at`, `updated_at` | timestamptz | |

### `push_tokens`

One row per device that can receive pushes.

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `user_id` | uuid FK → users, on delete cascade | |
| `token` | text, unique, not null | Expo push token |
| `platform` | text | `ios` / `android` / `web` |
| `created_at`, `last_seen_at` | timestamptz | Prune stale tokens using `last_seen_at` and Expo's delivery receipts |

### `households`

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `name` | text, not null | |
| `created_by` | uuid FK → users | |
| `created_at`, `updated_at` | timestamptz | |

Deleting a household (owner only) cascades to members, invites, custom catalog items, entries and events.

### `household_members`

| Column | Type | Notes |
|---|---|---|
| `household_id` | uuid FK → households, cascade | PK part 1 |
| `user_id` | uuid FK → users, cascade | PK part 2 |
| `role` | `member_role` (`owner`, `member`) | |
| `joined_at` | timestamptz | |

- Exactly one owner per household: partial unique index on `(household_id) WHERE role = 'owner'`.
- Index on `(user_id)` for "which households am I in?" (household switcher).
- **Leaving:** members can leave (delete their own row). The owner cannot leave; they must transfer ownership first, or delete the household.
- **Transferring ownership:** done by the owner in one transaction: demote the current owner to `member`, then promote the new owner. The order matters because the one-owner index is checked on each statement. The new owner must already be a member.

### `household_invites`

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `household_id` | uuid FK → households, cascade | |
| `code` | text, unique, not null | Short, human-typable (e.g. 8 chars, no ambiguous characters). Link form: `https://<host>/join/<code>` |
| `created_by` | uuid FK → users | |
| `created_at` | timestamptz | |
| `expires_at` | timestamptz, not null | e.g. 7 days after creation |
| `revoked_at` | timestamptz | Set when revoked |

The same code works as both the typed code and the link, can be reused by several people until it expires, and can be revoked. Only the owner can create or revoke invites.

### `categories` and `locations`

Built-in lookup tables with the same shape:

| Column | Type | Notes |
|---|---|---|
| `id` | smallint PK | |
| `key` | text, unique | Stable identifier, e.g. `dairy`, `fridge` |
| `name` | text | Display name |
| `sort_order` | smallint | |

Seed data: categories (Baking, Produce, Dairy, Grains, Spices, …) and locations (Pantry, Fridge, Freezer, Spice rack, …). Custom locations per household are planned for a later version (custom categories are not planned yet). Adding them means adding a nullable `household_id`, as `catalog_items` already has, and switching the IDs to a type that households can create rows for.

### `catalog_items`

Built-in ingredients and household custom items in one table.

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `household_id` | uuid FK → households, cascade, **nullable** | `NULL` = built-in; set = custom item, private to that household |
| `name` | text, not null | |
| `default_category_id` | smallint FK → categories, not null | |
| `default_location_id` | smallint FK → locations, not null | |
| `shelf_life_days` | integer | Typical shelf life; used to suggest an expiry date. `NULL` = no suggestion |
| `created_by` | uuid FK → users | `NULL` for built-ins |
| `created_at`, `updated_at` | timestamptz | |

- Unique names: unique index on `(household_id, lower(name))` (with `NULLS NOT DISTINCT`, so built-in names are unique too). A custom item cannot use the same name as a built-in one; the API checks this.
- Type-ahead: `pg_trgm` GIN index on `lower(name)`. The search returns built-ins plus the current household's custom items.
- "Set defaults once, remembered for the household": for a custom item, the defaults are stored on its own row when it is created and can be edited later. For a built-in item, they are stored in `household_item_defaults`.

### `household_item_defaults`

A household's own default category and location for a **built-in** catalog item.

| Column | Type | Notes |
|---|---|---|
| `household_id` | uuid FK → households, cascade | PK part 1 |
| `catalog_item_id` | uuid FK → catalog_items, cascade | PK part 2. Must be a built-in item (the API checks this); custom items keep their defaults on their own row |
| `category_id` | smallint FK → categories | `NULL` = use the built-in default |
| `location_id` | smallint FK → locations | `NULL` = use the built-in default |
| `updated_by` | uuid FK → users, on delete set null | |
| `updated_at` | timestamptz | |

Any member can set these. **Effective defaults** for an item in a household = `COALESCE(override, catalog default)`, computed in the item queries (a SQL view keeps this in one place).

### `pantry_entries`

One row per purchase ("Milk (Oct 20)", "Milk (Oct 27)").

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | Client-generated UUIDv7 |
| `household_id` | uuid FK → households, cascade | |
| `catalog_item_id` | uuid FK → catalog_items | Every entry points at a catalog item; typing a new name creates a custom catalog item first |
| `location_id` | smallint FK → locations, not null | Defaults from the item's effective default location; can be changed per entry (one milk in the fridge, one in the freezer) |
| `level` | `stock_level` (`full`, `half`, `low`, `out`) | `out` entries stay visible |
| `purchased_on` | date, not null, default today | Label in the expanded group ("Milk (Oct 20)") |
| `expires_on` | date | Optional; prefilled from `purchased_on + shelf_life_days` |
| `created_by`, `updated_by` | uuid FK → users, on delete set null | |
| `created_at`, `updated_at` | timestamptz | `updated_at` is also the "last write wins" clock for future offline sync |

- **Grouping** ("Milk ×2") is by `catalog_item_id`.
- **Category** comes from the item's effective default category for the household, not from the entry, so a group never splits across categories. Changing it re-categorizes every entry of that item.
- **Indexes:** `(household_id, catalog_item_id)` for the grouped view; `(household_id, expires_on) WHERE expires_on IS NOT NULL` for expiry highlighting and the nightly push job.

### `item_events`

Append-only change log. It powers the "Set to Low by Sam, 2h ago" label now and the activity feed later.

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | Client-generated UUIDv7. Also makes retries idempotent: a repeated insert is ignored |
| `household_id` | uuid FK → households, cascade | |
| `entry_id` | uuid, **no FK** | Survives deletion of the entry |
| `catalog_item_id` | uuid, no FK | Lets the feed show the item name even after deletion (with `snapshot`) |
| `actor_id` | uuid FK → users, on delete set null | |
| `type` | `event_type` | `entry_created`, `level_changed`, `expiry_changed`, `location_changed`, `entry_deleted` |
| `changes` | jsonb | e.g. `{"level": {"from": "half", "to": "low"}}` |
| `snapshot` | jsonb | Entry state after the event (name, level, location, expiry), so the feed does not need joins and works for deleted entries |
| `occurred_at` | timestamptz, not null | |

- The API writes the event in the **same transaction** as the entry change.
- The app's database role gets INSERT and SELECT only on this table, never UPDATE or DELETE.
- **Indexes:** `(entry_id, occurred_at DESC)` for the latest-change label; `(household_id, occurred_at DESC)` for the future feed.
- The "Set to Low by Sam, 2h ago" label: a lateral join to the latest event per entry.

### `expiry_notifications`

Stops the nightly job from sending the same alert twice.

| Column | Type | Notes |
|---|---|---|
| `entry_id` | uuid | PK part 1 (no FK; delete rows with the entry, or let old rows age out) |
| `user_id` | uuid FK → users, cascade | PK part 2 |
| `expires_on` | date | PK part 3; if the expiry date is edited, a new alert can be sent |
| `sent_at` | timestamptz | |

## Access control

- The Go API verifies the Firebase ID token, looks up `users.id`, and at the start of each transaction runs `SET LOCAL app.user_id = '<uuid>'`.
- RLS policy on household-scoped tables (`households`, `household_members`, `household_invites`, `household_item_defaults`, `pantry_entries`, `item_events`):

  `household_id IN (SELECT household_id FROM household_members WHERE user_id = current_setting('app.user_id')::uuid)`

- `catalog_items`: the same policy, OR `household_id IS NULL` (built-ins are readable by everyone, writable by no one through the app role).
- Owner-only actions (create or revoke invites, remove a member, transfer ownership, delete the household) are checked in the API, with a stricter RLS policy as a second check.
- Joining by invite code runs as a narrow privileged function (`SECURITY DEFINER`), because the user is not a member yet.

## Expiry rules (v1)

- **"Expiring soon":** `expires_on <= today + 3 days` (a constant in v1; it could become a per-user setting later).
- **Nightly job:** for each user with `push_enabled`, at about 9am in their `timezone`, find soon-to-expire entries in their households that are not `out`, skip ones already in `expiry_notifications`, then send one summary push per household.
