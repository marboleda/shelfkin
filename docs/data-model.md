# Shelfkin — Data Model (v1 draft)

*Draft — October 26, 2026*

Postgres (Cloud SQL). Tables that hold household data are scoped by row-level security (see [Access control](#access-control)): `households` by its own `id`, and the other household tables by their `household_id` column. `expiry_notifications` has no household column; like `users` and `push_tokens`, it is scoped by the API. `push_tickets` is used only by the expiry job.

## Conventions

- **IDs:** UUIDv7 (time-ordered). The client generates them for `pantry_entries` and `item_events`, so full offline editing later needs no schema change; the server generates all others. Server-generated `id` columns default to Postgres's built-in `uuidv7()` (Postgres 18+). Client-generated IDs (`pantry_entries.id`, `item_events.id`) have **no** default, so an insert without an `id` fails. `categories` and `locations` use `smallint` IDs set explicitly in the seed, with no default and no sequence.
- **Timestamps:** `timestamptz`, stored in UTC. Calendar dates (expiry, purchase) are `date`. Every "row written at" timestamp (`created_at`, `updated_at`, `joined_at`, `last_seen_at`, `occurred_at`, `sent_at`) is `timestamptz NOT NULL DEFAULT now()`. Keeping `updated_at` (and `last_seen_at`) current on writes is the API's job; there are no triggers. Other timestamps (`expires_at`, `revoked_at`) have no default.
- **Nullability:** a column marked PK is not null. Every other column is nullable unless its Type cell below says "not null"; "not null" is never implied. Defaults are listed in the Type cell too; apart from the `uuidv7()` ID default above, a column with no default listed has none.
- **Enums:** Postgres enum types for small fixed sets (`member_role`, `stock_level`, `event_type`).
- **Deletes:** Hard deletes. History survives because `item_events` keeps the IDs and a snapshot, not foreign keys to deleted rows.

## Overview

```
users ─┬─< household_members >─── households ─┬─< household_invites
       │                                       ├─< catalog_items (custom) ──< pantry_entries
       │                                       ├─< household_item_defaults >── catalog_items (built-in)
       ├─< push_tokens ──< push_tickets        ├─< pantry_entries
       │                                       └─< item_events
       └─ (actor on entries / events)

catalog_items (built-in, household_id NULL) ──< pantry_entries
categories, locations (built-in lookup tables)
deleted_accounts (Firebase UIDs of recently deleted accounts, no FKs)
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
| `push_enabled` | boolean, not null, default false | Push notifications are opt-in. The setting is per user, not per device: turning notifications off on one device means removing that device's row from `push_tokens` |
| `created_at`, `updated_at` | timestamptz, not null, default `now()` | |

- **Deleting an account (#46):** `DELETE /me` deletes the `users` row through `delete_account()` (see [Access control](#access-control)). This cascades to the user's `household_members`, `push_tokens` (and their `push_tickets`) and `expiry_notifications`, and sets `created_by`, `updated_by` and `actor_id` to `NULL` on rows in households that remain. Households the user owns alone are deleted with them. If they own a household that has other members, nothing is deleted until they transfer ownership. The API deletes the Firebase user in the same transaction and rolls back if that fails.

### `deleted_accounts`

The Firebase UIDs of accounts deleted in the last day (#46). A Firebase ID token is verified without calling Firebase, so a token issued before the deletion stays valid until it expires (at most an hour). The auth middleware refuses to create a user for a UID listed here, so that token gets 401 instead of recreating the account.

| Column | Type | Notes |
|---|---|---|
| `firebase_uid` | text PK | |
| `deleted_at` | timestamptz, not null, default `now()` | Rows older than a day are deleted by `delete_account()` |

- **Indexes:** `(deleted_at)` for pruning.
- No RLS; `shelfkin_app` can only read it. Only `delete_account()` writes it.

### `push_tokens`

One row per device that can receive pushes. Deleting a device's token (on sign-out, or to turn notifications off on just that device) stops pushes to it; `users.push_enabled` turns them off for all of the user's devices.

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `user_id` | uuid FK → users, on delete cascade, not null | |
| `token` | text, unique, not null | Expo push token |
| `platform` | text | `ios` / `android` / `web`; validated by the API, not the database |
| `created_at`, `last_seen_at` | timestamptz, not null, default `now()` | The expiry job deletes tokens whose `last_seen_at` is more than 90 days old, and tokens whose delivery receipt says `DeviceNotRegistered` (#47) |

- **Indexes:** `(user_id)` (non-unique) for per-user token lookups and for the `on delete cascade` when a user is deleted; Postgres does not index foreign-key columns on its own. Added by a later migration in #21.

### `push_tickets`

Expo push tickets waiting for their delivery receipt (#47). When Expo accepts a push it returns a ticket ID; the receipt for it is ready about 15 minutes later. The expiry job stores one row per accepted push and, on later runs, fetches the receipts and deletes the row.

| Column | Type | Notes |
|---|---|---|
| `id` | text PK | Expo's ticket ID |
| `token_id` | uuid FK → push_tokens, on delete cascade, not null | The token the push was sent to; deleting the token deletes its tickets |
| `sent_at` | timestamptz, not null, default `now()` | Set by the job to its run time |

- **Indexes:** `(token_id)` for the `on delete cascade`; `(sent_at)` for finding tickets due for a receipt and tickets that aged out.

### `households`

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `name` | text, not null | |
| `created_by` | uuid FK → users, on delete set null | Nullable; `NULL` once the creator deletes their account (#46) |
| `created_at`, `updated_at` | timestamptz, not null, default `now()` | |

Deleting a household (owner only) cascades to members, invites, custom catalog items, entries and events.

### `household_members`

| Column | Type | Notes |
|---|---|---|
| `household_id` | uuid FK → households, cascade, not null | PK part 1 |
| `user_id` | uuid FK → users, cascade, not null | PK part 2 |
| `role` | `member_role` (`owner`, `member`), not null | |
| `joined_at` | timestamptz, not null, default `now()` | |

- Exactly one owner per household: partial unique index on `(household_id) WHERE role = 'owner'`.
- Index on `(user_id)` for "which households am I in?" (household switcher).
- **Leaving:** members can leave (delete their own row). The owner cannot leave; they must transfer ownership first, or delete the household.
- **Transferring ownership:** done by the owner through the privileged `transfer_ownership` function (see [Access control](#access-control)), in one transaction: demote the current owner to `member`, then promote the new owner. The order matters because the one-owner index is checked on each statement. The new owner must already be a member.

### `household_invites`

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `household_id` | uuid FK → households, cascade, not null | |
| `code` | text, unique, not null | Short, human-typable (e.g. 8 chars, no ambiguous characters). Link form: `https://<host>/join/<code>` |
| `created_by` | uuid FK → users, on delete set null | Nullable; `NULL` once the creator deletes their account (#46) |
| `created_at` | timestamptz, not null, default `now()` | |
| `expires_at` | timestamptz, not null, no default | Set by the API, e.g. 7 days after creation |
| `revoked_at` | timestamptz | Set when revoked |

The same code works as both the typed code and the link, can be reused by several people until it expires, and can be revoked. Only the owner can create or revoke invites.

### `categories` and `locations`

Built-in lookup tables with the same shape:

| Column | Type | Notes |
|---|---|---|
| `id` | smallint PK, no default | Set explicitly in the seed (no `serial` or identity sequence), so IDs are the same in every database |
| `key` | text, unique, not null | Stable identifier, e.g. `dairy`, `fridge` |
| `name` | text, not null | Display name |
| `sort_order` | smallint, not null | |

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
| `created_by` | uuid FK → users, on delete set null | `NULL` for built-ins, and once the creator deletes their account (#46) |
| `created_at`, `updated_at` | timestamptz, not null, default `now()` | |

- Unique names: unique index on `(household_id, lower(name))` (with `NULLS NOT DISTINCT`, so built-in names are unique too). A custom item cannot use the same name as a built-in one; the API checks this.
- Type-ahead: `pg_trgm` GIN index on `lower(name)`. The search returns built-ins plus the current household's custom items.
- "Set defaults once, remembered for the household": for a custom item, the defaults are stored on its own row when it is created and can be edited later. For a built-in item, they are stored in `household_item_defaults`.

### `household_item_defaults`

A household's own default category and location for a **built-in** catalog item.

| Column | Type | Notes |
|---|---|---|
| `household_id` | uuid FK → households, cascade, not null | PK part 1 |
| `catalog_item_id` | uuid FK → catalog_items, cascade, not null | PK part 2. Must be a built-in item (the API checks this, and so does RLS; see [Access control](#access-control)); custom items keep their defaults on their own row |
| `category_id` | smallint FK → categories | `NULL` = use the built-in default |
| `location_id` | smallint FK → locations | `NULL` = use the built-in default |
| `updated_by` | uuid FK → users, on delete set null | |
| `updated_at` | timestamptz, not null, default `now()` | |

Any member can set these. **Effective defaults** for an item in a household = `COALESCE(override, catalog default)`, computed in the item queries (a SQL view keeps this in one place). The view is created `WITH (security_invoker = true)`, so RLS applies to whoever queries it; without this, a view runs with its owner's rights and would bypass RLS.

### `pantry_entries`

One row per purchase ("Milk (Oct 20)", "Milk (Oct 27)").

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK, no default | Client-generated UUIDv7 |
| `household_id` | uuid FK → households, cascade, not null | |
| `catalog_item_id` | uuid FK → catalog_items, not null | Every entry points at a catalog item; typing a new name creates a custom catalog item first |
| `location_id` | smallint FK → locations, not null | Defaults from the item's effective default location; can be changed per entry (one milk in the fridge, one in the freezer) |
| `level` | `stock_level` (`full`, `half`, `low`, `out`), not null, no default | The API always sets it. `out` entries stay visible |
| `purchased_on` | date, not null, default today | Label in the expanded group ("Milk (Oct 20)") |
| `expires_on` | date | Optional; prefilled from `purchased_on + shelf_life_days` |
| `created_by`, `updated_by` | uuid FK → users, on delete set null | |
| `created_at`, `updated_at` | timestamptz, not null, default `now()` | `updated_at` is also the "last write wins" clock for future offline sync |

- **Grouping** ("Milk ×2") is by `catalog_item_id`.
- **Category** comes from the item's effective default category for the household, not from the entry, so a group never splits across categories. Changing it re-categorizes every entry of that item.
- **Indexes:** `(household_id, catalog_item_id)` for the grouped view; `(household_id, expires_on) WHERE expires_on IS NOT NULL` for expiry highlighting and the nightly push job.

### `item_events`

Append-only change log. It powers the "Set to Low by Sam, 2h ago" label now and the activity feed later.

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK, no default | Client-generated UUIDv7. Also makes retries idempotent: a repeated insert is ignored |
| `household_id` | uuid FK → households, cascade, not null | |
| `entry_id` | uuid, **no FK**, not null | Survives deletion of the entry |
| `catalog_item_id` | uuid, no FK, not null | Lets the feed show the item name even after deletion (with `snapshot`) |
| `actor_id` | uuid FK → users, on delete set null | |
| `type` | `event_type`, not null | `entry_created`, `level_changed`, `expiry_changed`, `location_changed`, `purchase_date_changed`, `entry_deleted`. `purchase_date_changed` exists because users can edit an entry's purchase date, and every edit produces an event |
| `changes` | jsonb, not null, default `'{}'` | e.g. `{"level": {"from": "half", "to": "low"}}` |
| `snapshot` | jsonb, not null | Entry state after the event (name, level, location, expiry), so the feed does not need joins and works for deleted entries |
| `occurred_at` | timestamptz, not null, default `now()` | |

- The API writes the event in the **same transaction** as the entry change.
- The app's database role gets INSERT and SELECT only on this table, never UPDATE or DELETE.
- **Indexes:** `(entry_id, occurred_at DESC)` for the latest-change label; `(household_id, occurred_at DESC)` for the future feed.
- The "Set to Low by Sam, 2h ago" label: a lateral join to the latest event per entry.

### `expiry_notifications`

Stops the nightly job from sending the same alert twice.

| Column | Type | Notes |
|---|---|---|
| `entry_id` | uuid, not null | PK part 1 (no FK; delete rows with the entry, or let old rows age out) |
| `user_id` | uuid FK → users, cascade, not null | PK part 2 |
| `expires_on` | date, not null | PK part 3; if the expiry date is edited, a new alert can be sent |
| `sent_at` | timestamptz, not null, default `now()` | |

## Access control

- The Go API verifies the Firebase ID token and looks up `users.id`. At the start of each transaction it switches to the restricted `shelfkin_app` role with `SET LOCAL ROLE shelfkin_app` and sets `app.user_id` for that transaction (`SELECT set_config('app.user_id', $1, true)`, since `SET LOCAL` cannot take a bind parameter).
- Policies read the current user as `NULLIF(current_setting('app.user_id', true), '')::uuid`. On a pooled connection the setting reads as `''` after an earlier transaction set it, so a plain `::uuid` cast would error.
- Policies use a `SECURITY DEFINER` helper function (e.g. `app_user_household_ids()`, `STABLE`, with a fixed `search_path`) that returns the caller's household IDs. A policy on `household_members` that queried `household_members` directly would recurse ("infinite recursion detected in policy").
- RLS policy on household-scoped tables (`household_members`, `household_invites`, `household_item_defaults`, `pantry_entries`, `item_events`):

  `household_id IN (SELECT app_user_household_ids())`

  `households` has no `household_id` column, so it is scoped by its own `id` the same way.
- `catalog_items`: the same policy, OR `household_id IS NULL` (built-ins are readable by everyone, writable by no one through the app role).
- Foreign-key checks ignore RLS, so on their own they would let a member of household A write a row in A that points at household B's custom item. B could then no longer delete that item (or household B), and the foreign-key error would tell A whether an item ID exists. The API checks the item before writing; the `WITH CHECK` of two policies is the second check behind it (#73):
  - `pantry_entries`: `catalog_item_id` must be a built-in item (`household_id IS NULL`) or a custom item of the entry's own household. Not "any item the user can see": a member of A and C can see C's custom items but cannot put one in A's pantry.
  - `household_item_defaults`: `catalog_item_id` must be a built-in item; custom items keep their defaults on their own row.

  A missing item and another household's item both fail with the same RLS error (`42501`), so the error does not show whether an ID exists. The `USING` clauses are unchanged, so these checks apply only to new and updated rows.
- `users`, `push_tokens` and `expiry_notifications` have no RLS (they hold no household data) and are scoped by the API: every query filters by the caller's user ID. `push_tickets` has no RLS either; only the expiry job reads and writes it.
- Owner-only actions (create or revoke invites, remove a member, rename or delete the household) are checked in the API, with a stricter RLS policy as a second check. Transfer ownership is also owner-only, but it goes through `transfer_ownership` (below) because RLS would block it.
- `shelfkin_app` cannot insert into `households` or `household_members`, or update `household_members`, or delete `users`, directly. Four narrow privileged (`SECURITY DEFINER`) functions do this instead. Each reads the caller from `app.user_id`, fails if it is not set, has a fixed `search_path`, and `shelfkin_app` has `EXECUTE` on it and nothing more:
  - `create_household(name)` creates a household together with the caller's `owner` row (#14).
  - `transfer_ownership(household_id, new_owner_id)` demotes the owner and promotes a member (#15). It is needed because, as `shelfkin_app`, the owner loses owner rights after demoting themselves, so the RLS policy would block the promote.
  - `join_household(code)` joins a household by invite code (#16), because the user is not a member yet.
  - `delete_account()` deletes the caller's account and the households they own alone (#46), because `shelfkin_app` cannot delete `users` rows and RLS stops an owner deleting their own `household_members` row. If the caller owns a household that has other members, it deletes nothing and returns those households.

## Expiry rules (v1)

- **"Expiring soon":** `expires_on <= today + 3 days` (a constant in v1; it could become a per-user setting later).
- **Nightly job:** for each user with `push_enabled`, at about 9am in their `timezone`, find soon-to-expire entries in their households that are not `out`, skip ones already in `expiry_notifications`, then send one summary push per household.
- **Receipts and stale tokens (#47):** the job stores the ticket ID of every push Expo accepts in `push_tickets`. Each run first fetches the receipts for tickets at least 15 minutes old, at most 1,000 IDs per request. A `DeviceNotRegistered` receipt deletes the token; any other receipt error is logged with the ticket ID and the token is kept. A ticket is deleted once its receipt is processed, or after 24 hours without one. Each run also deletes tokens whose `last_seen_at` is more than 90 days old (the app refreshes it on start, #34). Errors in the tickets themselves, at send time, are handled when sending (#22).
