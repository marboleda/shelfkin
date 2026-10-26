# Shelfkin — v1 Backlog

*Created October 26, 2026*

Product decisions are in [plan.md](plan.md) and the schema is in [data-model.md](data-model.md). The repository layout (set up in task 1) is: `api/` for the Go API, `app/` for the Expo app (web, iOS, Android), `api/db/migrations/` for SQL migrations, and `openapi/` for the API spec. Every task should leave the full test suite passing.

## 1. Set up an empty project with a passing test
Goal: A repository with a Go API and an Expo app that each have one passing test.
Description: Create `api/` as a Go module with a tiny `main.go` and one trivial unit test, and `app/` as a TypeScript Expo project (Expo Router) with Jest and one trivial component test. Add a root README describing the layout and a single command (e.g. a Makefile target or root script) that runs both test suites. Add `.gitignore` files and an `.editorconfig`.

## 2. Run tests in CI
Goal: Every push and pull request runs the API and app test suites automatically.
Description: Add a GitHub Actions workflow with separate jobs for the Go API (`go vet`, `go test ./...`) and the Expo app (type check, lint, Jest). Cache Go modules and npm dependencies, and make the workflow fail on any error.

## 3. Local Postgres and migration tooling
Goal: Developers can start Postgres locally and apply migrations with one command each.
Description: Add a Docker Compose file running Postgres (matching the major version planned for Cloud SQL) and wire up goose for migrations in `api/db/migrations/`, with an empty first migration. Add a Go test helper that creates a fresh test database, runs all migrations and cleans up, plus one test that uses it.

## 4. Users and push tokens schema
Goal: Migrations create the `users` and `push_tokens` tables.
Description: Write a goose migration for the `users` and `push_tokens` tables exactly as described in `_docs/data-model.md`, including unique constraints and defaults (`push_enabled` false). Add a test that applies the migration and checks a user and a token can be inserted and that duplicate Firebase UIDs and duplicate tokens are rejected.

## 5. Households, members and invites schema
Goal: Migrations create `households`, `household_members` and `household_invites`.
Description: Write a migration for the three tables from `_docs/data-model.md`, including the `member_role` enum, cascading deletes and the partial unique index that allows only one owner per household. Add tests proving a second owner is rejected, demote-then-promote within one transaction succeeds, and deleting a household removes its members and invites.

## 6. Categories and locations with seed data
Goal: The built-in categories and storage locations exist in the database.
Description: Write a migration creating the `categories` and `locations` lookup tables from `_docs/data-model.md` and seeding them (e.g. Baking, Produce, Dairy, Grains, Spices… and Pantry, Fridge, Freezer, Spice rack…) with stable `key` values and a sort order. Add a test that checks the seeded keys exist.

## 7. Catalog and household item defaults schema
Goal: Migrations create `catalog_items`, `household_item_defaults` and the effective-defaults view.
Description: Write a migration for `catalog_items` (built-in rows have no `household_id`) and `household_item_defaults` from `_docs/data-model.md`, enable `pg_trgm`, and add the case-insensitive unique name index and the trigram index for type-ahead. Create a SQL view that returns each item's effective default category and location per household (household override first, catalog default second). Add tests for name uniqueness and for the view's fallback behavior.

## 8. Seed the built-in ingredient catalog
Goal: The catalog contains a useful starter set of common cooking and baking ingredients.
Description: Compile around 150–300 common ingredients, each with a default category, default location and typical shelf life in days (or none), and load them through a migration or a repeatable seed script. Keep the source data in a reviewable file (e.g. CSV) so it is easy to extend. Add a test that the seed loads cleanly and every row references valid categories and locations.

## 9. Pantry entries, events and notification log schema
Goal: Migrations create `pantry_entries`, `item_events` and `expiry_notifications`.
Description: Write a migration for the three tables from `_docs/data-model.md`, including the `stock_level` and `event_type` enums, the indexes for grouping, expiry lookups and latest-event queries, and the deliberate absence of foreign keys from events to entries. Add tests that an event survives deletion of its entry and that deleting a household removes its entries and events.

## 10. Row-level security and the app database role
Goal: The database itself prevents users from seeing or changing other households' data.
Description: Create a restricted application role and RLS policies keyed on `current_setting('app.user_id')`, as described in the Access control section of `_docs/data-model.md`, with INSERT/SELECT only on `item_events` and read-only access to built-in catalog rows. Add a Go helper that opens a transaction and runs `SET LOCAL app.user_id`. Write tests showing that a member of household A cannot read or write household B's rows, and that built-in catalog items are visible to everyone.

## 11. OpenAPI spec and code generation
Goal: The API contract lives in one OpenAPI file that generates both Go server code and a TypeScript client.
Description: Create `openapi/openapi.yaml` with shared conventions (error format, auth scheme, ID and date formats) and a health endpoint. Set up oapi-codegen for the Go server interface and openapi-typescript (or similar) for the app's client, with one command that regenerates both. Add a check (CI or test) that fails if generated code is out of date.

## 12. Go API server skeleton
Goal: The Go API starts, connects to Postgres and serves a health endpoint.
Description: Build the server around the generated OpenAPI interface with configuration from environment variables, a pgx connection pool, structured logging, graceful shutdown and a `/healthz` endpoint that checks the database. Set up sqlc for type-safe queries in `api/` with one example query. Add an HTTP test for the health endpoint.

## 13. Firebase Auth verification and current user
Goal: API requests are authenticated with Firebase ID tokens and mapped to a Shelfkin user.
Description: Add the Firebase Auth emulator to Docker Compose and middleware that verifies the bearer token, creates the `users` row on first sight (display name, email) and puts the user ID in the request context for the RLS transaction helper. Add `GET /me` and `PATCH /me` (display name, timezone). Test with emulator-issued tokens, including rejected and expired tokens.

## 14. Households API
Goal: Users can create, list, view, rename and delete households.
Description: Implement endpoints to create a household (the creator becomes owner), list the caller's households (for the household switcher), get one, rename it, and delete it (owner only). Add the endpoints to the OpenAPI spec first. Test the owner-only rule and that non-members get 404.

## 15. Household members API
Goal: Members can be listed and removed, members can leave, and ownership can be transferred.
Description: Implement listing members with display names and roles, removing a member (owner only), leaving a household (members only; the owner gets a clear error), and transferring ownership to an existing member in one transaction. The rules are in the Households section of `_docs/plan.md`. Test every rule, including that the owner cannot leave.

## 16. Invites API
Goal: Owners can create and revoke invite codes, and users can join a household with a code.
Description: Implement owner-only endpoints to create an invite (short, unambiguous code, 7-day expiry) and revoke it, plus a join endpoint that adds the caller as a member via a narrow `SECURITY DEFINER` database function, because the caller is not a member yet. Joining must fail for expired, revoked or unknown codes and be harmless for existing members. Test each case.

## 17. Catalog search API
Goal: The app can get type-ahead suggestions for ingredient names.
Description: Implement a search endpoint that returns built-in items plus the current household's custom items matching a partial name, ranked by trigram similarity and prefix match, with each item's effective default category, location and shelf life. Keep it fast enough for per-keystroke use (limit results; check the query plan). Test ranking and that other households' custom items never appear.

## 18. Custom items and household defaults API
Goal: Households can create custom catalog items and change default category and location for any item.
Description: Implement creating a custom item (name, category, location, optional shelf life), editing a custom item's defaults, and setting or clearing a household override for a built-in item in `household_item_defaults`. Reject custom names that collide with built-in or existing custom items. Test that overrides only affect the household that set them.

## 19. Pantry entry writes with change events
Goal: Entries can be created, updated and deleted, and every change is recorded in `item_events`.
Description: Implement create (client-supplied UUIDv7 ID, defaults filled from the item's effective defaults, suggested expiry from shelf life), update (level, expiry, location, purchase date) and delete endpoints. Each write records an `item_events` row in the same transaction, with `changes` and `snapshot`, and retries with the same IDs are idempotent. Test event contents for every event type and idempotent retries.

## 20. Pantry read API
Goal: The app can fetch a household's pantry, grouped and filterable, in one request.
Description: Implement an endpoint returning entries grouped by catalog item, with each item's effective category, each entry's location, level, dates and an expiring-soon flag (expiry within 3 days), plus the latest change ("Set to Low by Sam" with timestamp). Support filtering by location and by category. Test grouping, filters, the flag boundary and the last-change label.

## 21. Push token and notification settings API
Goal: Devices can register for push, and users can opt in or out.
Description: Implement registering or refreshing an Expo push token (updating `last_seen_at`), removing a token on sign-out, and toggling `push_enabled` on the user. Test that tokens are unique across users (re-registering a token moves it) and that opting out keeps tokens but stops sends.

## 22. Expiry notification job
Goal: A nightly job sends each opted-in user one summary push per household about items expiring soon.
Description: Build a Go command (deployable as a Cloud Run job) that finds, for users whose local time is around 9am, soon-to-expire entries that are not Out, skips ones already in `expiry_notifications`, sends one summary per household through the Expo Push API, and records what was sent. Handle invalid-token receipts by deleting those tokens. Test with a fake push sender, covering deduplication, time zones and edited expiry dates.

## 23. Screen designs
Goal: Agreed wireframes and a visual direction for every v1 screen.
Description: Design the sign-in, onboarding (create or join household), pantry list (grouped rows, location/category toggle, filters, expiry highlighting), add item, entry detail/edit, household settings and notification settings screens for phone and desktop widths. Define a small set of design tokens (colors, type scale, spacing) that is clean and simple but pleasant. Record the result in `_docs/` (images or links plus notes).

## 24. App shell, navigation and theme
Goal: The Expo app has its screen structure, navigation and design tokens in place on web and mobile.
Description: Set up Expo Router routes for the main screens (signed-out, onboarding, pantry, add item, entry detail, settings) as placeholders, with the layout adapting between phone and desktop widths. Implement the design tokens from the screen designs as a theme with light and dark mode. Add a smoke test that the root layout renders.

## 25. App API client and data layer
Goal: The app calls the API through a typed client with caching.
Description: Wire the TypeScript client generated from `openapi/openapi.yaml` into TanStack Query, attaching the Firebase ID token to each request and handling 401s by signing out. Add a base URL setting per environment (local, production) and shared error handling. Test with a mocked server (e.g. MSW).

## 26. Sign-in and sign-up in the app
Goal: Users can create an account, sign in and sign out on web and mobile.
Description: Integrate Firebase Auth in the Expo app (email/password at minimum; consider Google and Apple sign-in, which the App Store requires if other social logins are offered), using the Auth emulator in development. Call `GET /me` after sign-in and route signed-out users to the sign-in screen. Test the sign-in flow with mocked auth.

## 27. Household onboarding and switcher
Goal: New users can create or join a household, and members of several households can switch between them.
Description: Build the onboarding screen shown when a user has no households (create one, or enter an invite code) and a household switcher reachable from the pantry screen. Remember the last selected household on the device. Test the onboarding routing and switching.

## 28. Pantry list screen
Goal: Users see their household's pantry grouped by item, with expiring items highlighted.
Description: Build the main pantry list from the pantry read API: one row per item ("Milk ×2") that expands to show each entry with its level, purchase date, expiry and "Set to Low by Sam, 2h ago". Out entries stay visible but de-emphasized, and items expiring within 3 days are highlighted. Include empty and loading states, and component tests.

## 29. Location/category view toggle and filters
Goal: Users can view the pantry organized by location or by category and filter it.
Description: Add a toggle between "by location" and "by category" section grouping, plus filter chips for specific locations or categories. Remember the user's choice on the device. Test the grouping logic and filter combinations.

## 30. Add item flow
Goal: Users can add a pantry entry quickly with type-ahead suggestions.
Description: Build the add screen: a name field with debounced type-ahead from the catalog search API, prefilled location, level (default Full) and suggested expiry from shelf life, all editable. If the name is not in the catalog, offer to create a custom item and ask for its category and location once. Optimize for adding several items in a row, and test the flow with a mocked API.

## 31. Edit and delete entries
Goal: Users can change an entry's level, expiry, location and purchase date, or delete it.
Description: Build the entry detail/edit view with one-tap level changes (Full/Half/Low/Out) and editors for the other fields, generating client-side UUIDv7s for new entries as required by the API. Deleting asks for confirmation. Update the pantry list immediately (optimistic update) and roll back on error; test both paths.

## 32. Item defaults editing
Goal: Household members can change an item's default category and location.
Description: From an item's group row or detail view, let members edit the default category and location: this edits a custom item directly or sets a household override for a built-in item. Show which values come from the household and allow resetting a built-in item to its default. Test both item types against a mocked API.

## 33. Household settings screen
Goal: Members can manage the household from one screen, with owner-only actions shown only to the owner.
Description: Build a settings screen showing household name and members with roles. The owner can rename the household, create/copy/share and revoke invite links and codes, remove members, transfer ownership and delete the household (with confirmation); members can leave. Test that owner-only controls are hidden for members.

## 34. Invite deep links
Goal: Opening an invite link joins the household in the app if installed, or on the web otherwise.
Description: Handle `https://<host>/join/<code>` with Expo Router, keeping the code across sign-in if the user is signed out. Configure iOS universal links and Android app links (the `apple-app-site-association` and `assetlinks.json` files served from Firebase Hosting). Test the route handling, including the signed-out path.

## 35. Push notification opt-in in the app
Goal: Users can turn expiry push notifications on or off on each device.
Description: Add a notifications setting that requests OS permission, gets an Expo push token and registers it with the API, and turns `push_enabled` on or off. Tapping an expiry notification opens the relevant household's pantry. Remove the token on sign-out, and test the settings logic with mocked notification APIs.

## 36. Read-only offline mode
Goal: The last-synced pantry can be viewed without a connection, and edits are clearly disabled.
Description: Persist the TanStack Query cache to device storage (AsyncStorage on mobile, IndexedDB or localStorage on web), detect connectivity, show an offline banner and disable edit controls while offline. Show when the data was last synced. Test cache restore and the disabled-edit state.

## 37. Keeping data fresh
Goal: Changes made by other household members appear without restarting the app.
Description: Refetch the pantry when the app or browser tab regains focus and on a reasonable interval while visible, and add pull-to-refresh on mobile. Live updates over SSE are not in v1. Test that focus triggers a refetch.

## 38. GCP and Firebase infrastructure
Goal: Production infrastructure exists and is reproducible from code.
Description: Using Terraform, set up the GCP project with Cloud SQL for PostgreSQL, Artifact Registry, a Cloud Run service for the API, Secret Manager for credentials, and a Firebase project with Auth and Hosting. Use least-privilege service accounts and connect Cloud Run to Cloud SQL. Document how to apply it in the README.

## 39. API deployment pipeline
Goal: Merges to main build, migrate and deploy the API automatically.
Description: Add a GitHub Actions workflow that builds the API container, pushes it to Artifact Registry, runs goose migrations against Cloud SQL and deploys to Cloud Run, authenticating with Workload Identity Federation instead of stored keys. Add a rollback note to the README.

## 40. Web app deployment
Goal: The web app is published to Firebase Hosting on every merge to main.
Description: Add a workflow that builds the Expo static web export with production settings and deploys it to Firebase Hosting, along with the deep-link association files. Configure single-page-app fallback routing so links like `/join/<code>` load the app.

## 41. Schedule the expiry job in production
Goal: The expiry notification job runs automatically in production.
Description: Deploy the expiry job as a Cloud Run job and add a Cloud Scheduler trigger that runs it hourly (each run handles users whose local time is around 9am), in Terraform. Add alerting or log-based checks for failed runs.

## 42. Mobile builds and test distribution
Goal: iOS and Android test builds can be produced and installed by testers.
Description: Configure EAS Build and Submit with development, preview and production profiles, app identifiers, icons and splash screen, and production API settings. Ship a first build to TestFlight and Google Play internal testing, and document the release steps in the README.
