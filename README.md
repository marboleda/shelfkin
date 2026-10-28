# Shelfkin

Shelfkin is an app for managing a household pantry: the ingredients a household has for cooking and baking. It runs on the web, iOS and Android, and members of a household share one pantry.

## Repository layout

| Path | What it is |
|---|---|
| `api/` | Go HTTP API (deployed to Cloud Run) |
| `app/` | Expo (React Native) app for web, iOS and Android, using Expo Router; screens live in `app/src/app/` |
| `_docs/` | Product plan, data model and backlog |

SQL migrations live in `api/db/migrations/` (see [Local database](#local-database)). Planned addition: `openapi/` for the API spec.

## Requirements

- Go 1.26+
- Node.js 24+ and npm

## Getting started

Install the app's dependencies once:

```sh
npm --prefix app install
```

Run the app (press `w` for web, or scan the QR code with Expo Go):

```sh
npm --prefix app start
```

## Local database

The API uses PostgreSQL 18, the newest major version Cloud SQL for PostgreSQL offers; Cloud SQL must run the same major version. Locally it runs in Docker from [`compose.yaml`](compose.yaml), using a pinned `postgres:18.x` image. You need Docker with Compose v2. Migrations live in `api/db/migrations/` and are applied by a small Go program built on goose, so you do not need goose installed.

| Command | What it does |
|---|---|
| `npm run db:up` | Start Postgres and wait until it is healthy |
| `npm run db:migrate` | Apply all pending migrations (safe to run again) |
| `npm run db:status` | List migrations and when each was applied |
| `npm run db:down` | Stop Postgres, keeping its data |
| `npm run db:reset` | Stop Postgres and delete its data. Run `db:up` and `db:migrate` afterwards for a clean database |

Environment variables:

| Variable | Used by | Default |
|---|---|---|
| `SHELFKIN_DB_PORT` | `db:up`, and the local fallbacks below | `5432`. Set it (e.g. `55432`) if you already run Postgres locally |
| `DATABASE_URL` | `db:migrate`, `db:status` | The local compose database |
| `TEST_DATABASE_URL` | Go tests (`api/db/dbtest`) | The local compose database |

The local compose database is `postgres://shelfkin:shelfkin@localhost:5432/shelfkin?sslmode=disable`, with the port taken from `SHELFKIN_DB_PORT`. These credentials are for local development only and are not used anywhere else.

To set a variable for one session: `export SHELFKIN_DB_PORT=55432` in bash, or `$env:SHELFKIN_DB_PORT = "55432"` in PowerShell.

New migrations go in `api/db/migrations/` and are named `YYYYMMDDHHMMSS_name.sql` (UTC timestamp), with `-- +goose Up` and `-- +goose Down` sections. If your local database already has a newer migration applied than one you just pulled, run `npm run db:reset`.

Go tests that need a database call `dbtest.New(t)` from `api/db/dbtest`. It creates a database named `shelfkin_test_<random>`, applies every migration, returns a `pgxpool.Pool` and drops the database when the test ends. If Postgres is not reachable, the test fails, so run `npm run db:up` before `npm test`.

## Tests

Run every test suite from the repository root:

```sh
npm test
```

Or run one side:

```sh
npm run test:api   # go test ./... in api/
npm run test:app   # Jest in app/
```

## Docs

- [Product plan](_docs/plan.md)
- [Data model](_docs/data-model.md)
- [Backlog](_docs/tasks.md) (tracked as GitHub issues)
