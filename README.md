# Shelfkin

Shelfkin is an app for managing a household pantry: the ingredients a household has for cooking and baking. It runs on the web, iOS and Android, and members of a household share one pantry.

## Repository layout

| Path | What it is |
|---|---|
| `api/` | Go HTTP API (deployed to Cloud Run) |
| `app/` | Expo (React Native) app for web, iOS and Android, using Expo Router; screens live in `app/src/app/` |
| [`docs/`](docs/) | Product plan, data model and backlog |

Planned additions: `api/db/migrations/` for SQL migrations and `openapi/` for the API spec.

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

- [Product plan](docs/plan.md)
- [Data model](docs/data-model.md)
- [Backlog](docs/tasks.md) (tracked as GitHub issues)
