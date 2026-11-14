# Shelfkin

Shelfkin is an app for managing a household pantry: the ingredients a household has for cooking and baking. It runs on the web, iOS and Android, and members of a household share one pantry.

## Repository layout

| Path | What it is |
|---|---|
| `api/` | Go HTTP API (deployed to Cloud Run) |
| `infra/` | Terraform for the production GCP and Firebase project (see [Infrastructure](#infrastructure)) |
| `app/` | Expo (React Native) app for web, iOS and Android, using Expo Router; screens live in `app/src/app/`. The web build is served by Firebase Hosting (`firebase.json`, see [Deploying the web app](#deploying-the-web-app)) |
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
- `bash` and `curl` on `PATH`, and `gcloud` logged in with `gcloud auth login` (not only application-default credentials). `terraform apply` runs a bash script that turns on email enumeration protection with `curl` and `gcloud auth print-access-token` (see [Email enumeration protection](#email-enumeration-protection)). macOS and Linux have both. On Windows, install [Git for Windows](https://git-scm.com/download/win) and run `terraform` from Git Bash, so `bash` is Git's and not WSL's `C:\Windows\System32\bash.exe` (which cannot see the Windows `gcloud`)
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

Running `terraform plan` again after an apply should show "No changes" (check this on the first apply after a change to `infra/`). The Cloud Run service and job are created with a public placeholder image; deploys (#38) replace it and Terraform ignores image changes, so an apply never rolls a deploy back.

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
- The `authDomain` output is Firebase's default `<project_id>.firebaseapp.com`, which is **not** in the authorized domains (those are exactly `<hosting_site_id>.web.app`, `<hosting_site_id>.firebaseapp.com` and `localhost`). Decision (#39): the web build uses this default `authDomain` as is (`EXPO_PUBLIC_FIREBASE_AUTH_DOMAIN` = `firebase_web_config.authDomain`). That is fine because the app only uses email/password (`signInWithEmailAndPassword`, `createUserWithEmailAndPassword`, and `sendPasswordResetEmail` with no `continueUrl`), with no popup or redirect sign-in, so the auth domain is never loaded as a page from the site. Adding a popup or redirect provider (Google, Apple) would need this revisited: either use `<hosting_site_id>.firebaseapp.com` as `authDomain` or add the default domain to `authorized_domains` in `infra/firebase.tf`.
- Do not click "Get started" under Hosting in the Firebase console: it can create a default site named after the project and add its domains to the authorized domains. If a plan ever shows a change to `authorized_domains`, apply restores the list.

### IAM: Owner and Editor

Every member with `roles/owner` or `roles/editor` should be either a human admin (`user:`) or a Google-managed service agent that GCP grants automatically. Terraform grants Owner or Editor to none of the service accounts it creates, and it removes Editor from the Compute Engine default service account (`<project_number>-compute@developer.gserviceaccount.com`) if enabling an API creates it. After an apply, `gcloud projects get-iam-policy` still shows `roles/editor` for the **Google APIs Service Agent**, `<project_number>@cloudservices.gserviceaccount.com`. GCP grants it on every project and uses it to run internal Google processes for you. Google's docs say not to remove or change it, because that breaks managed instance groups, autoscaling and other services ([Compute Engine service accounts](https://cloud.google.com/compute/docs/access/service-accounts#google_apis_service_agent), [Service agents](https://cloud.google.com/iam/docs/service-agents)), so Terraform leaves it alone. To check after an apply, list the Owner and Editor members and confirm each is a `user:` or a Google-managed agent:

```sh
gcloud projects get-iam-policy "$PROJECT_ID" --flatten=bindings --filter='bindings.role:(roles/owner OR roles/editor)' --format='table(bindings.role, bindings.members)'
```

### Auth request logs

Identity Platform request logging is on (`monitoring.request_logging` in `infra/firebase.tf`), so every Firebase Auth API request is written to Cloud Logging in the project, including failed ones. This is the record of sign-in attempts by **disabled accounts**: the app shows them "Email or password is incorrect", the same as a wrong password, and logs nothing itself.

**Finding disabled-account attempts.** In [Logs Explorer](https://console.cloud.google.com/logs/query) for the project, run:

```
resource.type="identitytoolkit_project"
log_id("identitytoolkit.googleapis.com/requests")
"USER_DISABLED"
```

Then narrow it to sign-ins: the entry's method name ends in `SignInWithPassword`. The field paths below are expected but not yet confirmed against a real entry (nobody but the owner can see production logs). After the first apply, the owner opens the first real entry, corrects these paths and the query if needed, and removes this sentence:

```
resource.type="identitytoolkit_project"
log_id("identitytoolkit.googleapis.com/requests")
jsonPayload.methodName:"SignInWithPassword"
jsonPayload.status.message:"USER_DISABLED"
```

**Checking it in production.** In the Firebase console (Authentication > Users), add a throwaway user and disable it. Sign in with it on the production web app: the screen shows "Email or password is incorrect". Within a few minutes the query above shows the entry with the user's email. Then delete the throwaway user.

**Cost.** Request logs go to the `_Default` log bucket, which keeps them for 30 days. Cloud Logging includes 50 GiB per project per month for free, then charges per GiB ingested. Shelfkin's Auth traffic is far below that, but every Auth request is logged: sign-in, sign-up, token refresh and account lookup. If the volume ever matters, add an exclusion filter to the `_Default` sink that drops successful `identitytoolkit.googleapis.com/requests` entries and keeps only errors.

**PII.** Each entry contains the user's email address and the caller's IP address. Firebase does not log passwords; the owner confirms this in the first real entry and records it here. Anyone with `roles/logging.viewer` (or Owner or Editor) on the project can read the entries. They are deleted after the 30-day retention, and nothing exports them anywhere else.

### Email enumeration protection

Email enumeration protection ("improved email privacy") is on, so Firebase's sign-in and password reset responses do not reveal whether an email has an account: a wrong password and an unknown email both return `auth/invalid-credential`, and a password reset succeeds for unknown emails. Sign-up still says when an email is taken (`auth/email-already-in-use`); that is accepted.

The Google provider has no argument for this setting, so `terraform_data.email_privacy` in `infra/firebase.tf` sets it with a `PATCH` to the Identity Toolkit admin API when it is first created (or its project changes). Terraform does **not** read the setting back, so it will not notice if someone turns protection off in the console. Check it after the first apply, and whenever in doubt:

```sh
curl -sS -H "Authorization: Bearer $(gcloud auth print-access-token)" \
  -H "x-goog-user-project: $PROJECT_ID" \
  "https://identitytoolkit.googleapis.com/admin/v2/projects/$PROJECT_ID/config" | grep -A2 emailPrivacyConfig
# "emailPrivacyConfig": { "enableImprovedEmailPrivacy": true }
```

If it shows `false` or no `emailPrivacyConfig`, run the `PATCH` again with `terraform apply -replace='terraform_data.email_privacy[0]'`.

The `email_privacy_patch` variable only exists so the offline `terraform test` never runs the `PATCH`. Leave it at its default (`true`) in `terraform.tfvars`.

### Rotating the database passwords

The passwords of `shelfkin_api` and `shelfkin_migrator` are generated by Terraform and kept only in Secret Manager (`api-database-url`, `migrator-database-url`) and the Terraform state.

**`shelfkin_api`.** There is no Cloud SQL user for it (see [Database roles](#database-roles-in-production)), so the apply only replaces the `api-database-url` version. Run Deploy API by hand **straight after**:

```sh
terraform -chdir=infra apply -replace=random_password.api
gh workflow run deploy-api.yml --ref main
```

That deploy's migrate job cannot log in with the new URL (`28P01`), so it sets the new password on `shelfkin_api` (as a SCRAM verifier) and prints `Set a new password for shelfkin_api`, checks the login (`Checked: shelfkin_api can log in`), and then the workflow deploys `shelfkin-api`. Cloud Run reads `latest` secret versions when an instance starts, so instances of the new revision use the new password.

**Accepted gap (v1):** rotation causes a short outage. Between the apply and the migrate job, new API instances and expiry job runs get the new URL while the role still has the old password, so they cannot connect. Between the `ALTER ROLE` and the new revision, the old instances still hold the old password and cannot open new connections. Rotating without an outage would need two logins used in turn, which is not planned.

**Is there a new revision?** Cloud Run creates a revision only when the service's revision template changes, and the deploy changes only the image. The workflow builds the image again on a fresh runner and deploys it by digest; the build records its creation time in the image config, so the digest, and with it the template, normally differs from the serving one. Do not rely on that: after the deploy, check that the newest revision was created after the rotation:

```sh
gcloud run revisions list --service shelfkin-api --region "$REGION" --limit 3   # see the CREATED column
```

If no new revision was created, start one by deploying the same image by its tag instead of its digest. The image string changes, so Cloud Run creates a revision, and Terraform ignores the image (see `infra/run.tf`), so `terraform plan` still shows "No changes". The next regular deploy goes back to the digest:

```sh
gcloud run deploy shelfkin-api --region "$REGION" --quiet \
  --image "$REGION-docker.pkg.dev/$PROJECT_ID/shelfkin/api:$(git rev-parse origin/main)"
gcloud run services update-traffic shelfkin-api --region "$REGION" --to-latest --quiet
```

**`shelfkin_migrator`** is still a Cloud SQL built-in user, so rotating it is unchanged:

```sh
terraform -chdir=infra apply -replace=random_password.migrator
```

This sets the new password on the Cloud SQL user and replaces the `migrator-database-url` version. `shelfkin-migrate` reads the latest version on every execution, so it needs nothing more. Never run `gcloud sql users set-password` for `shelfkin_migrator`: the secret would no longer match.

### Destroying

Cloud SQL has deletion protection on, so `terraform destroy` fails until it is turned off. Set both `deletion_protection = false` and `settings.deletion_protection_enabled = false` in `infra/database.tf`, run `terraform apply`, then `terraform destroy`. This deletes the database and its backups. A deleted Workload Identity pool keeps its ID reserved for 30 days. The state bucket, the project and its billing link are not managed by Terraform.

## Deploying the web app

`.github/workflows/deploy-web.yml` builds the Expo web app with `npx expo export --platform web` and publishes `app/dist` to Firebase Hosting, at `https://shelfkin-5da75.web.app` (and `https://shelfkin-5da75.firebaseapp.com`). It authenticates with Workload Identity Federation as `github-deployer`, the same as [Deploying the API](#deploying-the-api), and uses no Firebase CI token, key or secret. `firebase-tools` is pinned in the workflow (`FIREBASE_TOOLS_VERSION`).

The Hosting config is in `firebase.json` and `.firebaserc`:

- `app/app.json` sets `web.output` to `single`: the export is one `index.html` plus hashed bundles, and Expo Router picks the screen in the browser. Every path that is not a file is rewritten to `/index.html`, so `/settings`, `/entries/<id>` and `/join/<code>` open directly or on reload, and an unknown path shows the app's "Page not found" screen (Hosting returns 200 for it).
- `/_expo/**`, `/assets/**` and `/.well-known/**` are never rewritten, so a missing bundle or association file is a real 404, not the app's HTML.
- HTML and every other unhashed file are served with `Cache-Control: no-cache`, so a reload after a deploy gets the new version. Hashed files under `/_expo/` and `/assets/` get `public, max-age=31536000, immutable`.
- `/.well-known/apple-app-site-association` and `/.well-known/assetlinks.json` are served as `application/json` from `app/public/.well-known/` (added in #33). `ignore` in `firebase.json` is empty on purpose: the default would drop dotfiles (`.well-known`) and `node_modules` paths (Expo puts font and image assets under `assets/node_modules/`).
- `firebase.json` deploys the Hosting target `web`, and `.firebaserc` maps it to the site `shelfkin-5da75` in project `shelfkin-514420`. The workflow fails if that mapping differs from the `FIREBASE_HOSTING_SITE` variable.

### What triggers a web deploy

- **Automatically**, when the `CI` workflow succeeds for a push to `main`. It builds the exact commit CI tested. A failed CI run, a pull request (including from a fork) or any other branch deploys nothing. As with the API, a run whose commit is no longer the tip of `main` deploys nothing and says so in its summary; the newer commit deploys when its CI passes.
- **By hand**: Actions > Deploy web > Run workflow, on `main` (or `gh workflow run deploy-web.yml --ref main`). It deploys the commit `main` points to. Other branches are refused.

Only one web deploy runs at a time. It runs independently of the API deploy.

### Actions variables

Set these as repository Actions **variables** (Settings > Secrets and variables > Actions > Variables), from `terraform output` (see [Outputs to GitHub Actions and EAS variables](#outputs-to-github-actions-and-eas-variables)). None is a secret; the Firebase web API key ships in the bundle.

| Variable | Value |
|---|---|
| `GCP_PROJECT_ID`, `GCP_WIF_PROVIDER`, `GCP_DEPLOYER_SA` | Shared with the API deploy |
| `FIREBASE_HOSTING_SITE` | `hosting_site_id` (`shelfkin-5da75`) |
| `EXPO_PUBLIC_API_URL` | `api_url`, the Cloud Run URL (same value as `API_URL`) |
| `EXPO_PUBLIC_FIREBASE_API_KEY` | `firebase_web_config.apiKey` |
| `EXPO_PUBLIC_FIREBASE_AUTH_DOMAIN` | `firebase_web_config.authDomain` |
| `EXPO_PUBLIC_FIREBASE_PROJECT_ID` | `firebase_web_config.projectId` |

If any of them is missing or blank, the run fails before building, so a site that would only show the app's configuration error screen is never published. The build also forces `EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST` empty and fails if the configured values were not inlined into the bundle.

### What a web deploy does

1. Checks the variables and the Hosting target, checks out the commit, runs `npm ci` and `npx expo export --platform web` in `app/` with the variables above.
2. Runs `firebase deploy --only hosting:web` with the message `Commit <sha>`, which creates a new release on the live channel.
3. Smoke checks on `https://<site>.web.app`: `/`, `/settings`, `/entries/abc`, `/join/ABCDEFGH` and `/nope` return 200 with the app's HTML and `no-cache`; the entry bundle is `immutable`; each association file is either a 404 (with a warning, until #33 adds it) or 200 JSON. Any other result fails the run (the release is already live then; roll back below if needed).

The run summary shows the commit SHA, the site and the Hosting release (`sites/<site>/versions/<version>`).

### Rolling back the web app

Every deploy is a release in the site's history, labelled `Commit <sha>`. To go back:

- Firebase console > Hosting > `shelfkin-5da75` > Release history, find the last good release, then the three-dot menu > **Rollback**. This makes that version live again at once.
- Or from the command line, copying an earlier version to the live channel (needs Firebase Hosting Admin):

  ```sh
  # <version id> is the last part of sites/shelfkin-5da75/versions/<version id> (run summary or release history)
  npx firebase-tools@15.34.2 hosting:clone shelfkin-5da75@<version id> shelfkin-5da75:live --project shelfkin-514420
  ```

- Or revert the change on `main` and let CI deploy it.

The next deploy from `main` replaces a rolled-back release. Viewing the release history needs at least Firebase Hosting Viewer on the project.

### Production checks after the first web deploy

Run these once and record the results on #39:

- `curl -sI https://shelfkin-5da75.web.app/<path>` for `/`, `/settings`, `/entries/abc` and `/join/ABCDEFGH`: 200 with `cache-control: no-cache`. A hashed `/_expo/static/js/web/entry-*.js` has `immutable`.
- The first run's summary shows the commit SHA and the release `sites/shelfkin-5da75/versions/<id>`, and its smoke checks step passed.
- Signed out, opening `/settings` or `/entries/abc` in a fresh tab goes to `/sign-in?redirect=…` (the app's HTML, not a Hosting 404). `/nope` shows "Page not found".
- The release history shows the `Commit <sha>` message, and a rollback (above) works.
- Once #33 has landed:
  - `curl -sI` on `/.well-known/apple-app-site-association` and `/.well-known/assetlinks.json` shows 200, `content-type: application/json` and no redirect, and the body is the committed file.
  - Opening `/join/ABCDEFGH` in a fresh tab, and reloading, shows the invite screen. Until #33 it shows "Page not found".
- Once the API is deployed:
  - Signed in, opening `/`, `/settings` and `/entries/abc` in a fresh tab, and reloading, lands on that screen.
  - Settings shows "Server: Connected".
  - A new user can sign up and sign in against the production Firebase project.
  - The site also works at `https://shelfkin-5da75.firebaseapp.com`.
  - Send a real password-reset email from the sign-in screen. Its link goes to Firebase's default action handler on `<project_id>.firebaseapp.com`; confirm it opens, the new password can be set, and sign-in works with it. This matters because no default Hosting site should exist for the project (see [Outputs to GitHub Actions and EAS variables](#outputs-to-github-actions-and-eas-variables)).

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
2. Points the `shelfkin-migrate` job at the new image and runs `migrate up` with `--wait`, logged in as `shelfkin_migrator`. After the migrations it creates the `shelfkin_api` login if it is missing, or sets its password if the `api-database-url` password no longer logs in, then grants `shelfkin_app` to `shelfkin_api`, and finally logs in as `shelfkin_api` once to check that the API will be able to connect (see [Database roles](#database-roles-in-production)). Once done, this changes nothing. If anything fails, the workflow stops and the API is **not** deployed.
3. Deploys `shelfkin-api` with the new image. Only the image changes: env vars, secrets, scaling and the startup probe stay as Terraform set them. If the new revision fails its startup probe (`GET /health`), Cloud Run keeps serving the previous revision and the workflow fails. Traffic is then sent to the latest revision, which also undoes an earlier rollback.
4. Smoke checks: `GET <API_URL>/health` must return 200 with `{"status":"ok"}` and `GET <API_URL>/me` without a token must return 401 in the `Error` format. Either failing fails the workflow (the new revision is already serving then; roll back below if needed).

The job and the migrate binary are chosen per execution (`gcloud run jobs execute --args=/usr/local/bin/migrate,up --update-env-vars=API_DB_ROLE=shelfkin_api`), so the job's definition stays as Terraform made it apart from its image, which Terraform ignores. `--update-env-vars` adds to the job's env, so `DATABASE_URL` and `API_DATABASE_URL` (from the `migrator-database-url` and `api-database-url` secrets, set by Terraform) stay set.

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

The API and the expiry job log in as `shelfkin_api`, which must be able to do **less** than `shelfkin_app` on its own: `dbtx` runs `SET LOCAL ROLE shelfkin_app`, and SQL in that transaction could run `RESET ROLE`.

`shelfkin_api` is not a Cloud SQL user: Cloud SQL makes every built-in user a member of `cloudsqlsuperuser`, granted as `cloudsqladmin`, and no role we have can revoke that (#38, #92). Instead the migrate job creates it with SQL (`api/cmd/migrate/apilogin.go`, enabled by `API_DATABASE_URL`, which Terraform sets from `api-database-url`). After the migrations and before the grant step:

- if `shelfkin_api` does not exist, `shelfkin_migrator` runs `CREATE ROLE shelfkin_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOREPLICATION PASSWORD '<verifier>'` and prints `Created login role shelfkin_api`. The new role is a member of nothing, and `shelfkin_migrator`, as its creator, may later change its password. (Stock Postgres 16+ records that as an `ADMIN` grant of `shelfkin_api` to `shelfkin_migrator` in `pg_auth_members`; Cloud SQL does not show that row, as step 0 on #92 found. Neither the job nor the grant step reads it or depends on it.);
- if it exists, the job logs in with `API_DATABASE_URL`. If that works it prints `shelfkin_api can log in; password unchanged`. If the password is wrong (`28P01`) it runs `ALTER ROLE shelfkin_api PASSWORD '<verifier>'` and prints `Set a new password for shelfkin_api`. Any other login error, or an `ALTER ROLE` that is refused (as on a role `shelfkin_migrator` did not create, such as the old built-in user), fails the job.

The password is never sent in SQL, because Cloud SQL logs the text of a failed statement. The job computes a SCRAM-SHA-256 verifier in Go (`SCRAM-SHA-256$4096:<salt>$<StoredKey>:<ServerKey>`, random salt) and Postgres stores it as it is, as `psql`'s `\password` does. Neither the password, the URL nor the verifier is ever printed. `API_DB_ROLE` must equal the URL's user name, or the job stops before it changes anything.

After the grant step passes, the job logs in with `API_DATABASE_URL` once more, checks that `current_user` is `shelfkin_api`, and prints `Checked: shelfkin_api can log in`. So the API is only deployed once its login is known to work.

The grant step itself (`api/cmd/migrate/grant.go`, enabled by `API_DB_ROLE`) is unchanged. After every `migrate up` it:

- grants `shelfkin_app` to `shelfkin_api` `WITH INHERIT FALSE, SET TRUE` if no such grant exists yet (or fixes the options of its own grant), so `shelfkin_api` has `shelfkin_app`'s privileges only after `SET ROLE` and none after `RESET ROLE`. This needs `shelfkin_migrator` to have `ADMIN` on `shelfkin_app`, which it has because the #10 migration created the role as that user;
- tries to revoke every other grant of `shelfkin_app` to `shelfkin_api` that is not `INHERIT FALSE, SET TRUE` without `ADMIN`, every other role membership (such as `cloudsqlsuperuser`), and the `CREATEDB`, `CREATEROLE`, `BYPASSRLS` and `REPLICATION` attributes;
- then **fails the migrate job** (so the API is not deployed) if `shelfkin_api` is still `SUPERUSER`, `CREATEDB`, `CREATEROLE`, `BYPASSRLS` or `REPLICATION`, is a member of any role other than `shelfkin_app`, has any `shelfkin_app` grant that inherits, lacks `SET` or has `ADMIN` (or no right one at all), owns any object at all, holds a direct grant on any object, or is named in a policy. Ownership and grants are read from `pg_shdepend`, which records every owned object and every ACL entry naming the role, of every kind (tables, columns, types and domains including domains over arrays, functions, schemas, statistics objects, collations, text search objects, operator families, publications, foreign servers, large objects, languages, parameters, default privileges, tablespaces, databases, ...) in this database, in shared catalogs and in every other database. The only exception is `CONNECT` and `TEMPORARY` on a database, which every login needs. The error names each object (with `pg_describe_object`) and each membership's grantor, and points to [Moving `shelfkin_api` off the Cloud SQL built-in user](#moving-shelfkin_api-off-the-cloud-sql-built-in-user) below.

Grants to `PUBLIC` are not grants to `shelfkin_api`, so the step does not look at them. The production check `RESET ROLE; SELECT FROM users;` below is what catches a table or column readable through `PUBLIC`.

Why per grantor: on Postgres 16 and later, `pg_auth_members` keeps **one row per grantor**, and `GRANT`/`REVOKE` only touch the row of the role running them. A second `GRANT shelfkin_app TO shelfkin_api WITH INHERIT TRUE` made by an admin is a separate row that `shelfkin_migrator`'s own `GRANT` does not change. So the step revokes each row `GRANTED BY` its own grantor, and the check reads every row again. Postgres only lets a role revoke a grant made by a role whose privileges it has, so `shelfkin_migrator` (not a superuser on Cloud SQL) cannot revoke grants made by `postgres` or by Cloud SQL itself. Those make the step fail rather than pass.

Do not weaken this check to get a deploy through.

### Moving `shelfkin_api` off the Cloud SQL built-in user

**This applies once, to the existing production instance only.** A fresh project needs no admin step: Terraform no longer creates a Cloud SQL user for `shelfkin_api`, so the first deploy creates it with SQL and passes end to end.

History: production's `shelfkin_api` was created by Terraform as a Cloud SQL built-in user, which Cloud SQL made a member of `cloudsqlsuperuser` as `cloudsqladmin`. Not even `postgres` could revoke that, so the grant step failed closed (#38). It is replaced by a role the migrate job creates (#92). Nothing in production used `shelfkin_api` yet, so the short time with no `shelfkin_api` login during these steps is accepted. If the expiry job (#40) is already deployed, pause its scheduler until step 5 passes.

To connect as `postgres` (step 3 and the production checks below):

```sh
# Set a password for the built-in postgres user (only needed once)
gcloud sql users set-password postgres --instance shelfkin --prompt-for-password

# Connect through the Cloud SQL Auth Proxy (the instance accepts no direct connections)
cloud-sql-proxy "$(terraform -chdir=infra output -raw cloudsql_connection_name)" --port 5433 &
psql "host=127.0.0.1 port=5433 user=postgres dbname=shelfkin"
```

Do it in this order:

1. **Merge the change.** The automatic deploy that follows is expected to fail at the grant step, as before: the job does not have `API_DATABASE_URL` yet, and the old built-in user is still a `cloudsqlsuperuser` member. The API service is not touched.
2. **Apply Terraform from `main`:**

   ```sh
   git checkout main && git pull
   terraform -chdir=infra plan
   terraform -chdir=infra apply
   ```

   The plan must show exactly three changes: `google_sql_user.api` **forgotten** from state (not destroyed: it "will no longer be managed by Terraform"), the new `API_DATABASE_URL` env var on `google_cloud_run_v2_job.migrate`, and the new `google_secret_manager_secret_iam_member.migrate_runtime_api_url`. It must show no change to `google_secret_manager_secret_version.api_database_url`: the password and the secret stay the same. Afterwards `terraform -chdir=infra plan` shows "No changes". This must come **before** step 3: deleting the user while it is still in Terraform state would make the next apply create it again as a built-in user.
3. **Delete the built-in user.** As `postgres`, run the `pg_shdepend` query from [Production checks](#production-checks-after-the-first-deploy) for `shelfkin_api`. It must return no rows (memberships do not block the delete; `DROP ROLE` removes them). Then:

   ```sh
   gcloud sql users delete shelfkin_api --instance=shelfkin --project="$PROJECT_ID"
   ```

   If the delete is refused because objects depend on the role, revoke or reassign what the query lists, connected as its grantor (an owned object goes back with `ALTER ... OWNER TO shelfkin_migrator`), and retry.
4. **Deploy by hand:** `gh workflow run deploy-api.yml --ref main`. The migrate job's log must show `Created login role shelfkin_api`, `Granted shelfkin_app to shelfkin_api (INHERIT FALSE, SET TRUE)` and `Checked: shelfkin_api can log in`. The API then deploys and the smoke checks pass.
5. **Deploy by hand again.** The log shows `shelfkin_api can log in; password unchanged` and `shelfkin_api already has shelfkin_app (INHERIT FALSE, SET TRUE)`, and the run passes.

Then run every check in [Production checks](#production-checks-after-the-first-deploy) and record the results on #38. The membership check passes because the new role never had `cloudsqlsuperuser`, not because the check changed.

If the migrate job fails at `ALTER ROLE` with an error naming this section, the role it found is one `shelfkin_migrator` did not create, and the password in `api-database-url` does not log in: most likely the built-in user still exists. Do step 3, then deploy again. A grant-step failure naming a `cloudsqlsuperuser` membership means the same. Do not weaken the check, and do not create or change any table as `postgres`.

### Production checks after the first deploy

Run these once, connected as `postgres` through the Cloud SQL Auth Proxy (see the section above), and record the results on the deploy issue.

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

-- No rows: shelfkin_api owns nothing (o), has no direct grants (a) and is named in no policy (r),
-- in this database, in shared catalogs (tablespaces, databases, parameters) or any other database.
-- CONNECT and TEMPORARY on a database are allowed.
SELECT sd.deptype,
       CASE WHEN sd.dbid IN (0, (SELECT oid FROM pg_database WHERE datname = current_database()))
            THEN pg_describe_object(sd.classid, sd.objid, sd.objsubid)
            ELSE 'an object in database ' || (SELECT datname FROM pg_database WHERE oid = sd.dbid) END AS object
FROM pg_shdepend sd
WHERE sd.refclassid = 'pg_authid'::regclass AND sd.refobjid = 'shelfkin_api'::regrole
  AND sd.deptype IN ('o', 'a', 'r')
  AND NOT (sd.deptype = 'a' AND sd.classid = 'pg_database'::regclass AND NOT EXISTS (
    SELECT FROM pg_database d, aclexplode(d.datacl) a
    WHERE d.oid = sd.objid AND a.grantee = sd.refobjid AND a.privilege_type NOT IN ('CONNECT', 'TEMPORARY')));

-- If the query above lists grants: which privilege each one is and who granted it
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
  UNION ALL SELECT 'tablespace ' || t.spcname, a.* FROM pg_tablespace t, aclexplode(t.spcacl) a
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

Then connect as `shelfkin_api` (`user=shelfkin_api`; its password is in the `api-database-url` secret, which must name `shelfkin_api`). Each of these must fail with `permission denied` (42501). Keep the zero-column `SELECT`: it succeeds if `shelfkin_api` can read the table or any single column, whether granted directly or to `PUBLIC`, so it catches column grants and `GRANT ... TO PUBLIC`, which the grant step does not look at (`SELECT *` would miss a grant on only some columns):

```sql
BEGIN; SET LOCAL ROLE shelfkin_app; RESET ROLE; SELECT FROM household_members; ROLLBACK;
BEGIN; SET LOCAL ROLE shelfkin_app; RESET ROLE; SELECT FROM users; ROLLBACK;
```

## Docs

- [Product plan](docs/plan.md)
- [Data model](docs/data-model.md)
- [Backlog](docs/tasks.md) (tracked as GitHub issues)
