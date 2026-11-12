# Shelfkin

Shelfkin is an app for managing a household pantry: the ingredients a household has for cooking and baking. It runs on the web, iOS and Android, and members of a household share one pantry.

## Repository layout

| Path | What it is |
|---|---|
| `api/` | Go HTTP API (deployed to Cloud Run) |
| `infra/` | Terraform for the production GCP and Firebase project (see [Infrastructure](#infrastructure)) |
| `app/` | Expo (React Native) app for web, iOS and Android, using Expo Router; screens live in `app/src/app/` |
| `openapi/` | The API contract, `openapi/openapi.yaml` (OpenAPI 3.0.3), that the Go server interface and the app's types are generated from (see [API spec and generated code](#api-spec-and-generated-code)) |
| [`docs/`](docs/) | Product plan, data model and backlog |

SQL migrations live in `api/db/migrations/` (see [Local database](#local-database)).

## Requirements

- Go 1.26+
- Node.js 24+ and npm

## Getting started

Install the app's dependencies once:

```sh
npm --prefix app install
```

Run the app (press `w` for web, or scan the QR code with Expo Go). To sign in locally, start the API and the Auth emulator first; see [Sign-in and the Auth emulator](app/README.md#sign-in-and-the-auth-emulator):

```sh
npm --prefix app start
```

## Local database

The API uses PostgreSQL 18, the newest major version Cloud SQL for PostgreSQL offers; Cloud SQL must run the same major version. Locally it runs in Docker from [`compose.yaml`](compose.yaml), using a pinned `postgres:18.x` image. You need Docker with Compose v2. Migrations live in `api/db/migrations/` and are applied by a small Go program built on goose, so you do not need goose installed.

| Command | What it does |
|---|---|
| `npm run db:up` | Start Postgres and the Firebase Auth emulator (see [Auth emulator](#auth-emulator)) and wait until both are healthy |
| `npm run db:migrate` | Apply all pending migrations (safe to run again) |
| `npm run db:status` | List migrations and when each was applied |
| `npm run db:down` | Stop Postgres (keeping its data) and the auth emulator |
| `npm run db:reset` | Stop Postgres and the auth emulator and delete Postgres's data. Run `db:up` and `db:migrate` afterwards for a clean database |

Environment variables:

| Variable | Used by | Default |
|---|---|---|
| `SHELFKIN_DB_PORT` | `db:up`, and the local fallbacks below | `5432`. Set it (e.g. `55432`) if you already run Postgres locally |
| `DATABASE_URL` | `db:migrate`, `db:status`, and the API server (see [Running the API](#running-the-api)) | The local compose database for `db:migrate` and `db:status`. The API server has no default and requires it |
| `TEST_DATABASE_URL` | Go tests (`api/db/dbtest`) | The local compose database |
| `SHELFKIN_AUTH_PORT` | `db:up` (host port of the auth emulator), and the Go tests' local fallback | `9099` |
| `TEST_AUTH_EMULATOR_HOST` | Go tests (`api/internal/auth/authtest`): the auth emulator as `host:port` | `localhost:9099`, with the port taken from `SHELFKIN_AUTH_PORT` |

The local compose database is `postgres://shelfkin:shelfkin@localhost:5432/shelfkin?sslmode=disable`, with the port taken from `SHELFKIN_DB_PORT`. These credentials are for local development only and are not used anywhere else.

To set a variable for one session: `export SHELFKIN_DB_PORT=55432` in bash, or `$env:SHELFKIN_DB_PORT = "55432"` in PowerShell.

New migrations go in `api/db/migrations/` and are named `YYYYMMDDHHMMSS_name.sql` (UTC timestamp), with `-- +goose Up` and `-- +goose Down` sections. If your local database already has a newer migration applied than one you just pulled, run `npm run db:reset`.

Go tests that need a database call `dbtest.New(t)` from `api/db/dbtest`. It creates a database named `shelfkin_test_<random>`, applies every migration, returns a `pgxpool.Pool` and drops the database when the test ends. If Postgres is not reachable, the test fails, so run `npm run db:up` before `npm test`. The same goes for the auth emulator integration tests, which fail rather than skip when the emulator is not running.

## Running the API

The API server is `api/main.go`. It reads its configuration from environment variables:

| Variable | Required | Default | What it is |
|---|---|---|---|
| `DATABASE_URL` | Yes | None | Postgres connection string, as a URL (`postgres://user:password@host:port/db?sslmode=...`) or `key=value` pairs. If it sets no `connect_timeout`, each new connection gives up after 5s |
| `FIREBASE_PROJECT_ID` | Yes | None | The Firebase project whose ID tokens the API accepts. `demo-shelfkin` locally (the auth emulator's project); the production project ID in production |
| `FIREBASE_AUTH_EMULATOR_HOST` | No | None | `host:port` of the Firebase Auth emulator, `localhost:9099` locally. When set, the API accepts the emulator's unsigned tokens and logs a warning at startup. **Never set it in production** (the #38 deploy must not pass it): anyone could then forge a token for any user |
| `PORT` | No | `8080` | Port to listen on, 1 to 65535. Cloud Run sets it |
| `LOG_LEVEL` | No | `info` | `debug`, `info`, `warn` or `error` |
| `CORS_ALLOWED_ORIGINS` | No | `http://localhost:8081` (the Expo web dev server) | Comma-separated browser origins allowed to call the API, each `http(s)://host[:port]` with no path, for example `https://shelfkin-5da75.web.app,https://shelfkin-5da75.firebaseapp.com`. Setting it replaces the default. `*` and wildcards are rejected |
| `APP_BASE_URL` | No | `http://localhost:8081` (the Expo web dev server) | Base URL of the app, used to build invite links as `<APP_BASE_URL>/join/<code>`. Must be an absolute `http` or `https` URL with no query or fragment; a trailing slash is ignored. Production sets it to the Hosting URL in `infra/run.tf` |

An empty variable counts as unset. If a variable is missing or invalid, the server logs one line naming it and exits with code 1. The database password is never logged. Every occurrence of the password in a log line is replaced with `[REDACTED]`; with the local credentials, where the password is the same as the user name, `shelfkin` is redacted wherever it appears.

To run it against the local compose database and auth emulator, from the repository root:

```sh
npm run db:up
npm run db:migrate
```

then in bash:

```sh
DATABASE_URL='postgres://shelfkin:shelfkin@localhost:5432/shelfkin?sslmode=disable' \
FIREBASE_PROJECT_ID=demo-shelfkin \
FIREBASE_AUTH_EMULATOR_HOST=localhost:9099 \
go -C api run .
```

or in PowerShell:

```powershell
$env:DATABASE_URL = 'postgres://shelfkin:shelfkin@localhost:5432/shelfkin?sslmode=disable'
$env:FIREBASE_PROJECT_ID = 'demo-shelfkin'
$env:FIREBASE_AUTH_EMULATOR_HOST = 'localhost:9099'
go -C api run .
```

Change `5432` if you set `SHELFKIN_DB_PORT`, and `9099` if you set `SHELFKIN_AUTH_PORT`. Check it with `curl http://localhost:8080/health`, which returns `{"status":"ok"}`, or 503 with code `database_unavailable` if the database does not answer within 2s. The server starts even when the database is down. Logs are JSON lines on stdout: a `listening` line with the port, one `request` line per request (method, path, status, `duration_ms`), and a `stopped` line after shutdown. Ctrl+C (or SIGTERM) stops it gracefully: it stops accepting connections, gives in-flight requests up to 9s to finish, closes the database pool and exits 0.

### Authentication

Every endpoint except `GET /health` needs a Firebase ID token in an `Authorization: Bearer <token>` header. Without one, or with a token that is malformed, expired or issued for another project, the API returns 401 with code `unauthorized`. The first request from a Firebase user creates their Shelfkin user: the display name comes from the token's `name` claim, else the part of the email before `@`, else `Shelfkin user`, and the time zone starts as `UTC`. Later requests copy a changed email from the token but never overwrite the display name. `GET /me` returns the caller's profile and `PATCH /me` changes `displayName` and `timezone`.

Tokens are verified with the Firebase Admin SDK, which caches Google's public keys; it does not check for revoked tokens, since ID tokens expire within an hour.

### Auth emulator

Locally, and in tests and CI, the API uses the [Firebase Auth emulator](https://firebase.google.com/docs/emulator-suite/connect_auth), never a real Firebase project. It runs as the `auth` service in `compose.yaml` (`firebase-tools` pinned to an exact version, built into a small Node image), so you need neither Java nor a global `firebase-tools`. `npm run db:up` starts it together with Postgres; the first run builds the image, which takes a minute. It serves the demo project `demo-shelfkin` (the `demo-` prefix means it can never reach a real project) on `localhost:9099`, or the port in `SHELFKIN_AUTH_PORT`. Its users live in memory and disappear when it stops.

Create a test user and get an ID token (the emulator accepts any API key):

```sh
curl -s -X POST 'http://localhost:9099/identitytoolkit.googleapis.com/v1/accounts:signUp?key=any' \
  -H 'Content-Type: application/json' \
  -d '{"email":"sam@example.com","password":"password123","displayName":"Sam","returnSecureToken":true}'
```

The response's `idToken` is valid for an hour. For a fresh one for the same user, call `accounts:signInWithPassword` with the same body (without `displayName`). Then, with the API running as above:

```sh
TOKEN='<the idToken>'
curl -s http://localhost:8080/me -H "Authorization: Bearer $TOKEN"
# {"displayName":"Sam","email":"sam@example.com","id":"…","pushEnabled":false,"timezone":"UTC"}
curl -s -X PATCH http://localhost:8080/me -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"timezone":"America/Toronto"}'
```

In PowerShell, use `Invoke-RestMethod`, for example `$TOKEN = (Invoke-RestMethod -Method Post -Uri 'http://localhost:9099/identitytoolkit.googleapis.com/v1/accounts:signUp?key=any' -ContentType 'application/json' -Body '{"email":"sam@example.com","password":"password123","displayName":"Sam","returnSecureToken":true}').idToken` and then `Invoke-RestMethod http://localhost:8080/me -Headers @{ Authorization = "Bearer $TOKEN" }`.

The app uses the same emulator in development: see [Sign-in and the Auth emulator](app/README.md#sign-in-and-the-auth-emulator) for how to point it there (including from an Android emulator or a physical device). Password reset emails are not sent; the emulator prints each reset link in its log (`docker compose logs auth`) and lists it at `http://localhost:9099/emulator/v1/projects/demo-shelfkin/oobCodes`.

Go tests that need an authenticated caller do not need the emulator: build the server with `authtest.NewVerifier()` (`api/internal/auth/authtest`) and send `authtest.Bearer(v.Issue(auth.Token{UID: "alice", Name: "Alice"}))` as the `Authorization` header. Only the integration tests that check real token verification use the emulator.

### CORS

The web app is served from a different origin than the API, so the API sends CORS headers. For a request whose `Origin` is in `CORS_ALLOWED_ORIGINS`, every response (including 4xx and 5xx errors, so the app can read the error body) carries `Access-Control-Allow-Origin` set to that origin and `Vary: Origin`. A preflight (`OPTIONS` with `Access-Control-Request-Method`) from an allowed origin gets 204, allowing the methods `GET`, `POST`, `PATCH` and `DELETE` and the headers `Authorization` and `Content-Type`, cached for 10 minutes. Requests from any other origin are still served but get no CORS headers, so the browser does not let the page read the response. `*` is never sent, because requests carry a bearer token; `Access-Control-Allow-Credentials` is not sent either, since auth does not use cookies.

Locally the default allows `npx expo start --web` on `http://localhost:8081`. If you run Expo web on another port or open it as `http://127.0.0.1:8081`, set `CORS_ALLOWED_ORIGINS` to that origin (setting it replaces the default, so list `http://localhost:8081` too if you still need it).

In production set it to both Firebase Hosting origins of the web app:

```
CORS_ALLOWED_ORIGINS=https://<hosting_site_id>.web.app,https://<hosting_site_id>.firebaseapp.com
```

which is `https://shelfkin-5da75.web.app,https://shelfkin-5da75.firebaseapp.com` for the site ID in [Infrastructure](#infrastructure). Terraform sets this on the `shelfkin-api` Cloud Run service (`infra/run.tf`). Add any custom domain here if one is added later.

## Expiry notification job

`api/cmd/expiry-job` sends expiry push notifications. It runs once and exits, and is meant to run hourly as a Cloud Run job (#40). Each run handles every user with `push_enabled` and at least one push token whose local hour (from `users.timezone`, or UTC if that cannot be loaded) is 9, and sends them one push per household listing the items there that are not `out` and expire within 3 days (already expired ones included), via the [Expo Push API](https://docs.expo.dev/push-notifications/sending-notifications/). Each entry is announced to each user once per expiry date (recorded in `expiry_notifications`), so running it twice in the same hour sends nothing the second time. Each run also deletes `expiry_notifications` rows whose expiry date is more than 30 days ago.

It reads `DATABASE_URL` and `LOG_LEVEL` like the API server (see [Running the API](#running-the-api)), plus:

| Variable | Required | Default | What it is |
|---|---|---|---|
| `EXPO_PUSH_URL` | No | `https://exp.host/--/api/v2/push/send` | Expo's push endpoint. Override it to point at a fake server |
| `EXPO_ACCESS_TOKEN` | No | None | Sent as a bearer token when set (Expo's enhanced push security). Never logged |

Flags:

| Flag | What it does |
|---|---|
| `--now 2026-10-27T13:00:00Z` | Run as of this time (RFC 3339) instead of now, to test or replay a run. 13:00 UTC in late October is 9:00 in Toronto |
| `--dry-run` | Log each push that would be sent (user, household, title, body) and how many old rows would be deleted. Sends nothing and writes nothing |

To try it against the local compose database without sending anything, in bash:

```sh
DATABASE_URL='postgres://shelfkin:shelfkin@localhost:5432/shelfkin?sslmode=disable' go -C api run ./cmd/expiry-job --dry-run --now 2026-10-27T13:00:00Z
```

or in PowerShell:

```powershell
$env:DATABASE_URL = 'postgres://shelfkin:shelfkin@localhost:5432/shelfkin?sslmode=disable'; go -C api run ./cmd/expiry-job --dry-run --now 2026-10-27T13:00:00Z
```

Without `--dry-run` it sends real pushes to the tokens in the database. Logs are JSON lines on stdout and never contain push tokens; outside dry runs they contain no item names either. Every run ends with one `expiry job finished` line with `users_processed`, `pushes_sent` (messages Expo accepted, one per household per device) and `failures` (users whose processing failed). The process exits 1 if any user failed, and 0 otherwise. A failure for one user (a database error, an Expo error after 3 retries of HTTP 429 or 5xx, or every token rejected) does not stop the others; that user's entries are not recorded and are tried again at their next 9am. Tokens Expo reports as `DeviceNotRegistered` are deleted. All database access runs as `shelfkin_app` through `api/internal/dbtx`, as the user being processed, so row-level security applies.

## API spec and generated code

The API contract lives in one file, [`openapi/openapi.yaml`](openapi/openapi.yaml). Its `info.description` lists the shared conventions (IDs, timestamps, dates, error format, auth). Generated code:

| Generated file | Generated from | Generator |
|---|---|---|
| `api/internal/openapi/openapi.gen.go` | `openapi/openapi.yaml` | [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) v2: models and a strict server interface for `net/http`, configured by `api/oapi-codegen.yaml` |
| `app/src/api/schema.ts` | `openapi/openapi.yaml` | [openapi-typescript](https://openapi-ts.dev/): TypeScript types for paths, operations and schemas |
| `api/internal/db/*.go` (package `db`) | SQL queries in `api/db/queries/`, checked against the schema in `api/db/migrations/` | [sqlc](https://sqlc.dev/): type-safe Go functions using pgx/v5, configured by `api/sqlc.yaml` |

After changing the spec, a query or a migration, regenerate everything from the repository root and commit the result:

```sh
npm run generate
```

**Do not edit generated files by hand**; change the spec or the queries and regenerate instead. `npm test` fails, naming the stale files, if a generated file does not match its source, and also if `api/internal/db/` holds a file sqlc does not generate (`npm run generate` deletes such files). Generator versions are pinned (oapi-codegen and sqlc through the `tool` directive in `api/go.mod`, openapi-typescript as an exact devDependency of the app), so no global installs are needed, but `npm --prefix app install` must have been run. sqlc builds from source with `go tool sqlc` and needs no cgo: without cgo (the default on Windows) it parses PostgreSQL with a WebAssembly build of the Postgres parser, so it runs on Windows and Linux alike.

Queries go in `api/db/queries/*.sql`, each starting with a sqlc annotation such as `-- name: Ping :one`. sqlc reads only the `-- +goose Up` sections of the migrations.

## Tests

Run every test suite from the repository root:

```sh
npm test
```

Or run one side:

```sh
npm run check:generated  # generated code matches openapi/openapi.yaml
npm run test:codegen     # tests for the generate scripts in scripts/
npm run test:api         # go test ./... in api/
npm run test:app         # Jest in app/
```

## Infrastructure

The production GCP and Firebase project is defined in Terraform under `infra/`: Cloud SQL for PostgreSQL 18, Secret Manager, Artifact Registry, the `shelfkin-api` Cloud Run service and `shelfkin-migrate` job, service accounts, Workload Identity Federation for GitHub Actions, Firebase (Auth, Hosting and the web, Android and Apple apps) and a billing budget. `terraform apply` against an empty project (after the one-time bootstrap below) recreates all of it. Apply from a developer machine, never from CI.

Fixed values:

| What | Value |
|---|---|
| Region (every regional resource) | `us-central1` |
| Firebase Hosting site ID | `shelfkin-5da75` (served at `https://shelfkin-5da75.web.app`). `shelfkin` was already taken |
| iOS bundle identifier and Android package | `com.marboleda.shelfkin` |
| Terraform | 1.14.3 (pinned in `infra/versions.tf`) |

If the Hosting site ID is taken when first applied, pick another, then update `terraform.tfvars` and this table.

### Prerequisites

- A Google account with Owner on the target project, or permission to create one
- Billing Account User on the billing account (to link it) and Billing Account Costs Manager or Administrator (to create the budget)
- [gcloud CLI](https://cloud.google.com/sdk/docs/install) and [Terraform 1.14.3](https://developer.hashicorp.com/terraform/install)
- Admin on `marboleda/shelfkin`, to add the GitHub Actions variables. No repository secrets are needed

### One-time bootstrap

These steps are not done by Terraform: the project and its billing link must exist before anything can be created in it, the Firebase terms are accepted by a person, and Terraform cannot keep its state in a bucket it has not created yet.

```sh
PROJECT_ID=shelfkin-prod            # your project ID
BILLING_ACCOUNT=XXXXXX-XXXXXX-XXXXXX
REGION=us-central1

gcloud auth login
gcloud auth application-default login

# 1. Create the project (skip if it exists) and link billing
gcloud projects create "$PROJECT_ID"
gcloud billing projects link "$PROJECT_ID" --billing-account="$BILLING_ACCOUNT"
gcloud auth application-default set-quota-project "$PROJECT_ID"

# 2. The state bucket: versioned, uniform bucket-level access, no public access
gcloud storage buckets create "gs://$PROJECT_ID-tfstate" \
  --project="$PROJECT_ID" --location="$REGION" \
  --uniform-bucket-level-access --public-access-prevention
gcloud storage buckets update "gs://$PROJECT_ID-tfstate" --versioning
```

3. Accept the Firebase terms once with the same Google account: open the [Firebase console](https://console.firebase.google.com/) and follow the prompt (you do not need to create a project there).

### Init, plan and apply

```sh
cd infra
cp terraform.tfvars.example terraform.tfvars   # then fill in real values; it is gitignored
terraform init -backend-config="bucket=$PROJECT_ID-tfstate"
terraform plan
terraform apply
```

Running `terraform plan` again after an apply should show "No changes". The Cloud Run service and job are created with a public placeholder image; deploys (#38) replace it and Terraform ignores image changes, so an apply never rolls a deploy back.

CI runs `terraform fmt -check`, `terraform init -backend=false`, `terraform validate` and `terraform test` (all providers mocked) with no credentials. Run the same locally from `infra/`.

### Outputs to GitHub Actions and EAS variables

`terraform output` prints these. Copy them into the repository's Actions **variables** (Settings > Secrets and variables > Actions > Variables) and the EAS environment variables:

| Output | GitHub Actions variable | EAS environment variable |
|---|---|---|
| `project_id` | `GCP_PROJECT_ID` (#38) | |
| `region` | `GCP_REGION` (#38) | |
| `workload_identity_provider` | `GCP_WIF_PROVIDER` (#38) | |
| `deployer_service_account` | `GCP_DEPLOYER_SA` (#38) | |
| `api_url` | `API_URL` (#38), `EXPO_PUBLIC_API_URL` (#39) | `EXPO_PUBLIC_API_URL` (#41) |
| `hosting_site_id` | `FIREBASE_HOSTING_SITE` (#39) | |
| `firebase_web_config.apiKey` | `EXPO_PUBLIC_FIREBASE_API_KEY` (#39) | `EXPO_PUBLIC_FIREBASE_API_KEY` (#41) |
| `firebase_web_config.authDomain` | `EXPO_PUBLIC_FIREBASE_AUTH_DOMAIN` (#39) | `EXPO_PUBLIC_FIREBASE_AUTH_DOMAIN` (#41) |
| `firebase_web_config.projectId` | `EXPO_PUBLIC_FIREBASE_PROJECT_ID` (#39) | `EXPO_PUBLIC_FIREBASE_PROJECT_ID` (#41) |
| `firebase_android_app_id` | | `google-services.json` file variable (#41), downloaded with `firebase apps:sdkconfig android <id>` |
| `firebase_apple_app_id` | | Used by #41 for the iOS config and push |

`artifact_registry_repository` (the image path prefix), `cloudsql_connection_name` and `hosting_url` are for reference: #38 derives image names from the project and region variables. The Firebase web API key is not a secret; it ships in the web bundle.

Notes for the workflows and the app:

- Workload Identity only issues tokens to `marboleda/shelfkin` workflows on `refs/heads/main` triggered by `workflow_run` or `workflow_dispatch` (the deploy triggers of #38 and #39). A deploy workflow with any other trigger, such as `push` or `pull_request_target`, cannot authenticate; widen the condition in `infra/wif.tf` if one is needed.
- The `authDomain` output is Firebase's default `<project_id>.firebaseapp.com`, which is **not** in the authorized domains (those are exactly `<hosting_site_id>.web.app`, `<hosting_site_id>.firebaseapp.com` and `localhost`). Email/password sign-in does not need it. Redirect or popup sign-in flows would, so #39 should either use `<hosting_site_id>.firebaseapp.com` as `authDomain` or add the default domain to `authorized_domains` in `infra/firebase.tf`.
- Do not click "Get started" under Hosting in the Firebase console: it can create a default site named after the project and add its domains to the authorized domains. If a plan ever shows a change to `authorized_domains`, apply restores the list.

### IAM: Owner and Editor

Every member with `roles/owner` or `roles/editor` should be either a human admin (`user:`) or a Google-managed service agent that GCP grants automatically. Terraform grants Owner or Editor to none of the service accounts it creates, and it removes Editor from the Compute Engine default service account (`<project_number>-compute@developer.gserviceaccount.com`) if enabling an API creates it. After an apply, `gcloud projects get-iam-policy` still shows `roles/editor` for the **Google APIs Service Agent**, `<project_number>@cloudservices.gserviceaccount.com`. GCP grants it on every project and uses it to run internal Google processes for you. Google's docs say not to remove or change it, because that breaks managed instance groups, autoscaling and other services ([Compute Engine service accounts](https://cloud.google.com/compute/docs/access/service-accounts#google_apis_service_agent), [Service agents](https://cloud.google.com/iam/docs/service-agents)), so Terraform leaves it alone. To check after an apply, list the Owner and Editor members and confirm each is a `user:` or a Google-managed agent:

```sh
gcloud projects get-iam-policy "$PROJECT_ID" --flatten=bindings --filter='bindings.role:(roles/owner OR roles/editor)' --format='table(bindings.role, bindings.members)'
```

### Rotating the database passwords

The passwords of `shelfkin_api` and `shelfkin_migrator` are generated by Terraform and kept only in Secret Manager (`api-database-url`, `migrator-database-url`) and the Terraform state. To rotate one:

```sh
terraform apply -replace=random_password.api        # shelfkin_api
terraform apply -replace=random_password.migrator   # shelfkin_migrator
```

This sets the new password on the Cloud SQL user and replaces the secret version. The old password stops working at once. `shelfkin-migrate` reads the latest version on every execution, so it needs nothing more. `shelfkin-api` reads the secret when an instance starts, so redeploy it straight after (re-run the #38 deploy workflow) to start a revision with the new password.

### Destroying

Cloud SQL has deletion protection on, so `terraform destroy` fails until it is turned off. Set both `deletion_protection = false` and `settings.deletion_protection_enabled = false` in `infra/database.tf`, run `terraform apply`, then `terraform destroy`. This deletes the database and its backups. A deleted Workload Identity pool keeps its ID reserved for 30 days. The state bucket, the project and its billing link are not managed by Terraform.

## Deploying the API

`.github/workflows/deploy-api.yml` builds the API image, runs the migrations on Cloud SQL and deploys `shelfkin-api` to Cloud Run. It authenticates with Workload Identity Federation as `github-deployer`; it uses the Actions variables from [Outputs to GitHub Actions and EAS variables](#outputs-to-github-actions-and-eas-variables) (`GCP_PROJECT_ID`, `GCP_REGION`, `GCP_WIF_PROVIDER`, `GCP_DEPLOYER_SA`, `API_URL`) and no secrets.

Every migration runs as `shelfkin_migrator` through the `shelfkin-migrate` job, never by hand as `postgres` or another admin user. `shelfkin_migrator` must own every table and the `SECURITY DEFINER` helpers (#10), which only work when they run as the owner of `household_members`.

### What triggers a deploy

- **Automatically**, when the `CI` workflow succeeds for a push to `main`. It deploys the exact commit CI tested. A failed CI run, a pull request (including from a fork) or any other branch deploys nothing.
- **By hand**: Actions > Deploy API > Run workflow, on `main` (or `gh workflow run deploy-api.yml --ref main`). It deploys the commit `main` points to. Other branches are refused.

Only one deploy runs at a time and a running one is never cancelled, so a migration is never stopped half way. If several commits land while a deploy runs, only the newest waits.

CI runs can finish out of order, so an automatic deploy whose commit is no longer the tip of `main` deploys nothing: it builds nothing, does not authenticate to Google and ends without failing. The run shows a notice and a summary line naming the skipped commit. The newer commit deploys when its own CI passes.

**If the newer commit's CI fails**, the older commit is not deployed either, and production stays on the last deployed revision. To get a deploy out, either:

- fix or revert on `main`; its CI then deploys as usual, or
- run the workflow by hand, but only when the latest CI run on `main` is green, because a manual run deploys the tip of `main`, not the older commit.

### What a deploy does

1. Builds `api/Dockerfile` and pushes it to Artifact Registry as `<region>-docker.pkg.dev/<project>/shelfkin/api:<full commit SHA>`.
2. Points the `shelfkin-migrate` job at the new image and runs `migrate up` with `--wait`, logged in as `shelfkin_migrator`. After the migrations it grants `shelfkin_app` to `shelfkin_api` (see [Database roles](#database-roles-in-production)); this changes nothing once done. If anything fails, the workflow stops and the API is **not** deployed.
3. Deploys `shelfkin-api` with the new image. Only the image changes: env vars, secrets, scaling and the startup probe stay as Terraform set them. If the new revision fails its startup probe (`GET /health`), Cloud Run keeps serving the previous revision and the workflow fails. Traffic is then sent to the latest revision, which also undoes an earlier rollback.
4. Smoke checks: `GET <API_URL>/health` must return 200 with `{"status":"ok"}` and `GET <API_URL>/me` without a token must return 401 in the `Error` format. Either failing fails the workflow (the new revision is already serving then; roll back below if needed).

The job and the migrate binary are chosen per execution (`gcloud run jobs execute --args=/usr/local/bin/migrate,up --update-env-vars=API_DB_ROLE=shelfkin_api`), so the job's definition stays as Terraform made it apart from its image, which Terraform ignores.

### Where to watch it

- The run in GitHub Actions (Actions > Deploy API). Its summary shows the commit SHA, the image digest and the Cloud Run revision.
- Migration output: Cloud Run > Jobs > `shelfkin-migrate` > Executions, or `gcloud run jobs executions list --job shelfkin-migrate --region "$REGION"` and the execution's logs.
- The service: Cloud Run > `shelfkin-api` > Revisions and Logs, or `gcloud run services describe shelfkin-api --region "$REGION"`.

Viewing needs Viewer, Cloud Run Viewer and Logs Viewer on the project.

### Rolling back

Send traffic back to the previous revision:

```sh
gcloud run revisions list --service shelfkin-api --region "$REGION"   # find the last good one
gcloud run services update-traffic shelfkin-api --region "$REGION" --to-revisions=<revision>=100
```

The next deploy sends traffic to its new revision again.

**Migrations are forward-only and are never rolled back**, by a deploy or by hand. Rolling back the API leaves the newer schema in place, so every migration must keep the previous release working: add first (new tables, nullable or defaulted columns), and remove or rename only in a later release, once nothing uses the old shape.

### Running the migrate job by hand

Always through the job, so migrations run as `shelfkin_migrator` (see above), never by hand as `postgres` or another admin user.

```sh
# Show every migration and whether it is applied
gcloud run jobs execute shelfkin-migrate --region "$REGION" --wait --args=/usr/local/bin/migrate,status
```

The output is in the execution's logs.

### Database roles in production

The API and the expiry job log in as `shelfkin_api`, which must be able to do **less** than `shelfkin_app` on its own: `dbtx` runs `SET LOCAL ROLE shelfkin_app`, and SQL in that transaction could run `RESET ROLE`. After every `migrate up`, the grant step (`api/cmd/migrate/grant.go`, enabled by `API_DB_ROLE`):

- grants `shelfkin_app` to `shelfkin_api` `WITH INHERIT FALSE, SET TRUE` if no such grant exists yet (or fixes the options of its own grant), so `shelfkin_api` has `shelfkin_app`'s privileges only after `SET ROLE` and none after `RESET ROLE`. This needs `shelfkin_migrator` to have `ADMIN` on `shelfkin_app`, which it has because the #10 migration created the role as that user;
- tries to revoke every other grant of `shelfkin_app` to `shelfkin_api` that is not `INHERIT FALSE, SET TRUE` without `ADMIN`, every other role membership (such as `cloudsqlsuperuser`), and the `CREATEDB`, `CREATEROLE`, `BYPASSRLS` and `REPLICATION` attributes;
- then **fails the migrate job** (so the API is not deployed) if `shelfkin_api` is still `SUPERUSER`, `CREATEDB`, `CREATEROLE`, `BYPASSRLS` or `REPLICATION`, is a member of any role other than `shelfkin_app`, has any `shelfkin_app` grant that inherits, lacks `SET` or has `ADMIN` (or no right one at all), owns any relation, function, schema, type, large object or database, or holds direct grants on anything: relations (tables, views, sequences), single columns, functions, schemas, types, large objects, languages, foreign-data wrappers and servers, parameters, default privileges for future objects, or a database (beyond `CONNECT` and `TEMPORARY`). The error names each grant and who granted it, and points to the one-time admin step below.

Why per grantor: on Postgres 16 and later, `pg_auth_members` keeps **one row per grantor**, and `GRANT`/`REVOKE` only touch the row of the role running them. A second `GRANT shelfkin_app TO shelfkin_api WITH INHERIT TRUE` made by an admin is a separate row that `shelfkin_migrator`'s own `GRANT` does not change. So the step revokes each row `GRANTED BY` its own grantor, and the check reads every row again. Postgres only lets a role revoke a grant made by a role whose privileges it has, so `shelfkin_migrator` (not a superuser on Cloud SQL) cannot revoke grants made by `postgres` or by Cloud SQL itself. Those make the step fail rather than pass.

Do not weaken this check to get a deploy through.

### One-time admin step (first deploy)

Cloud SQL creates built-in users (the `google_sql_user` resources, including `shelfkin_api`) with `CREATEDB` and `CREATEROLE` and as members of `cloudsqlsuperuser`; #37 confirmed that production's `shelfkin_api` is a member. Terraform cannot remove that, and `shelfkin_migrator` most likely cannot either: Cloud SQL, not `shelfkin_migrator`, made the grant, and on Postgres 16 and later only a role with the grantor's privileges may revoke it, and only a role with `ADMIN` on `shelfkin_api` may `ALTER` it. (The grant step still tries, `GRANTED BY` the recorded grantor. If that grantor is a role `shelfkin_migrator` has the privileges of, the step revokes the membership itself; the error then lists only what is left.) So **the first deploy on a fresh Cloud SQL database is expected to fail**: it applies the migrations, then the grant step fails with "shelfkin_api can do more than shelfkin_app: it ... is a member of cloudsqlsuperuser (granted by ...)", before the API service is touched (it keeps its previous image). This is by design. Fix it once as an admin, as `postgres` through the Cloud SQL Auth Proxy:

```sh
# Set a password for the built-in postgres user (only needed once)
gcloud sql users set-password postgres --instance shelfkin --prompt-for-password

# Connect through the Cloud SQL Auth Proxy (the instance accepts no direct connections)
cloud-sql-proxy "$(terraform -chdir=infra output -raw cloudsql_connection_name)" --port 5433 &
psql "host=127.0.0.1 port=5433 user=postgres dbname=shelfkin"
```

First see every membership row of `shelfkin_api` and who granted it (the migrate job's error lists the same):

```sql
SELECT r.rolname, pg_get_userbyid(m.grantor) AS grantor, m.inherit_option, m.set_option, m.admin_option
FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid
WHERE m.member = 'shelfkin_api'::regrole;
```

Then revoke each row other than the `shelfkin_app` row with `inherit_option = f, set_option = t, admin_option = f`, naming its grantor. A plain `REVOKE` without `GRANTED BY` only removes a grant `postgres` made itself, and only warns otherwise. For `cloudsqlsuperuser`, with `<grantor>` from the query above:

```sql
REVOKE cloudsqlsuperuser FROM shelfkin_api GRANTED BY <grantor>;
ALTER ROLE shelfkin_api NOCREATEDB NOCREATEROLE;
-- Only if the query showed a wrong shelfkin_app row from another grantor:
-- REVOKE shelfkin_app FROM shelfkin_api GRANTED BY <grantor>;
-- Only if the error lists REPLICATION or BYPASSRLS:
-- ALTER ROLE shelfkin_api NOREPLICATION NOBYPASSRLS;
```

Run the membership query again: it must show only the `shelfkin_app` row (`f`, `t`, `f`), and `SELECT rolcreatedb, rolcreaterole, rolbypassrls, rolreplication FROM pg_roles WHERE rolname = 'shelfkin_api'` must show all `f`.

If the error lists **privileges granted directly** to `shelfkin_api` (each is named, for example `SELECT on column users.email`), find out who granted them (the direct-grants query under "Production checks" shows the grantor). Nothing in this repo grants anything to `shelfkin_api`. If a migration did, fix it with a new migration that revokes it (migrations are forward-only); otherwise revoke it as the role that granted it, for example `REVOKE SELECT (id, email) ON users FROM shelfkin_api;` connected as that role.

**If `postgres` gets `permission denied`** (for example "Only roles with privileges of role "cloudsqladmin" may revoke privileges granted by this role", or "permission denied to alter role"), stop. Stock Postgres 16+ refuses both to a role that is not a superuser, and `postgres` on Cloud SQL is not one; whether Cloud SQL lets `postgres` do it anyway has to be tried on the real instance. Record the exact error on the deploy issue and do not weaken the check: the API stays on its previous image (or undeployed) until `shelfkin_api` is fixed. The way out then is to stop creating `shelfkin_api` through the Cloud SQL API (a change to `infra/`) and create it with SQL instead (`CREATE ROLE shelfkin_api LOGIN PASSWORD ...` as `shelfkin_migrator`, which then has `ADMIN` on it and gives it no other membership). That needs a decision on the deploy issue first.

Do not create or change any table as `postgres`. Then **run the workflow by hand** (Actions > Deploy API > Run workflow on `main`, or `gh workflow run deploy-api.yml --ref main`). That run, and every later deploy, passes the grant step with no admin step.

### Production checks after the first deploy

Run these once, connected as above, and record the results on the deploy issue.

Every migration applied (in the execution's logs, no `Pending` rows):

```sh
gcloud run jobs execute shelfkin-migrate --region "$REGION" --wait --args=/usr/local/bin/migrate,status
```

```sql
-- The API connects as shelfkin_api, not shelfkin_migrator: run while the API is serving
SELECT DISTINCT usename FROM pg_stat_activity WHERE datname = 'shelfkin' AND application_name <> 'psql';

-- Only shelfkin_app
SELECT r.rolname FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid
WHERE m.member = 'shelfkin_api'::regrole;

-- Every row (one per grantor): inherit_option f, set_option t, admin_option f
SELECT pg_get_userbyid(m.grantor) AS grantor, m.inherit_option, m.set_option, m.admin_option
FROM pg_auth_members m WHERE m.member = 'shelfkin_api'::regrole;

-- All false
SELECT rolsuper, rolcreatedb, rolcreaterole, rolbypassrls, rolreplication FROM pg_roles WHERE rolname = 'shelfkin_api';

-- No rows: nothing granted directly to shelfkin_api, including single columns
-- (column grants are in pg_attribute.attacl, not pg_class.relacl)
WITH g AS (
  SELECT 'relation ' || c.oid::regclass AS obj, a.* FROM pg_class c, aclexplode(c.relacl) a
  UNION ALL SELECT 'column ' || c.oid::regclass || '.' || quote_ident(att.attname), a.*
    FROM pg_attribute att JOIN pg_class c ON c.oid = att.attrelid, aclexplode(att.attacl) a
  UNION ALL SELECT 'function ' || p.oid::regprocedure, a.* FROM pg_proc p, aclexplode(p.proacl) a
  UNION ALL SELECT 'schema ' || n.nspname, a.* FROM pg_namespace n, aclexplode(n.nspacl) a
  UNION ALL SELECT 'type ' || t.oid::regtype, a.* FROM pg_type t, aclexplode(t.typacl) a
  UNION ALL SELECT 'large object ' || l.oid, a.* FROM pg_largeobject_metadata l, aclexplode(l.lomacl) a
  UNION ALL SELECT 'language ' || l.lanname, a.* FROM pg_language l, aclexplode(l.lanacl) a
  UNION ALL SELECT 'foreign-data wrapper ' || w.fdwname, a.* FROM pg_foreign_data_wrapper w, aclexplode(w.fdwacl) a
  UNION ALL SELECT 'foreign server ' || s.srvname, a.* FROM pg_foreign_server s, aclexplode(s.srvacl) a
  UNION ALL SELECT 'parameter ' || p.parname, a.* FROM pg_parameter_acl p, aclexplode(p.paracl) a
  UNION ALL SELECT 'database ' || d.datname, a.* FROM pg_database d, aclexplode(d.datacl) a
    WHERE a.privilege_type NOT IN ('CONNECT', 'TEMPORARY')
  UNION ALL SELECT 'default privileges (' || d.defaclobjtype::text || ') of ' || pg_get_userbyid(d.defaclrole), a.*
    FROM pg_default_acl d, aclexplode(d.defaclacl) a
)
SELECT obj, privilege_type, pg_get_userbyid(grantor) AS grantor FROM g WHERE grantee = 'shelfkin_api'::regrole;

-- Every table is owned by shelfkin_migrator, none by shelfkin_api (or the owner column of \dt)
SELECT tableowner, count(*) FROM pg_tables WHERE schemaname = 'public' GROUP BY tableowner;

-- Both helpers and household_members owned by shelfkin_migrator
SELECT proname, pg_get_userbyid(proowner) FROM pg_proc WHERE proname LIKE 'app_user_%';
SELECT tableowner FROM pg_tables WHERE tablename = 'household_members';

-- f: no FORCE ROW LEVEL SECURITY
SELECT relforcerowsecurity FROM pg_class WHERE relname = 'household_members';
```

Then connect as `shelfkin_api` (`user=shelfkin_api`; its password is in the `api-database-url` secret, which must name `shelfkin_api`). Each of these must fail with `permission denied` (42501). Keep the zero-column `SELECT`: it succeeds if `shelfkin_api` has `SELECT` on the table or on any single column, so it also catches a column grant (`SELECT *` would not, for a grant on some columns only):

```sql
BEGIN; SET LOCAL ROLE shelfkin_app; RESET ROLE; SELECT FROM household_members; ROLLBACK;
BEGIN; SET LOCAL ROLE shelfkin_app; RESET ROLE; SELECT FROM users; ROLLBACK;
```

## Docs

- [Product plan](docs/plan.md)
- [Data model](docs/data-model.md)
- [Backlog](docs/tasks.md) (tracked as GitHub issues)
