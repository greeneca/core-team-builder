# Development Guide

## Prerequisites

- Docker + Docker Compose (primary workflow)
- Go 1.25+ (optional, for running/building the backend outside Docker)

## Running with Docker (recommended)

```bash
cp .env.example .env          # then edit values as needed
docker compose up --build     # starts db, backend, seed, frontend
```

- Frontend: <http://localhost:8081>
- The `seed` service runs once on startup. Re-run it any time with:

```bash
docker compose run --rm seed
```

Tear down (and wipe the database volume):

```bash
docker compose down -v
```

## Environment variables

All configuration is via environment variables (see `.env.example`).

| Variable          | Used by   | Description                                        |
|-------------------|-----------|----------------------------------------------------|
| `POSTGRES_USER`   | db/backend| Database user                                      |
| `POSTGRES_PASSWORD`| db/backend| Database password                                 |
| `POSTGRES_DB`     | db/backend| Database name                                      |
| `DATABASE_URL`    | backend   | Full connection string (composed in compose)       |
| `HTTP_ADDR`       | backend   | Listen address (default `:8080`)                   |
| `JWT_SECRET`      | backend   | **Required.** Signing secret for tokens            |
| `JWT_TTL`         | backend   | Token lifetime (e.g. `24h`)                        |
| `CORS_ORIGIN`     | backend   | Allowed browser origin                             |
| `FRONTEND_PORT`   | frontend  | Host port for the site                             |
| `SEED_USERNAME`   | seed      | Test user username                                 |
| `SEED_EMAIL`      | seed      | Test user email                                    |
| `SEED_PASSWORD`   | seed      | Test user password                                 |

Generate a strong secret:

```bash
openssl rand -base64 48
```

## Running the backend without Docker

You still need a Postgres instance. Point `DATABASE_URL` at it:

```bash
cd backend
export DATABASE_URL="postgres://ctb:change-me-in-prod@localhost:5432/core_team_builder?sslmode=disable"
export JWT_SECRET="dev-only-insecure-secret-change-me"
export MIGRATIONS_DIR="../database/migrations"

go run ./cmd/seed      # apply schema + create test user
go run ./cmd/server    # start the API on :8080
```

Build binaries:

```bash
cd backend
go build ./...
go vet ./...
```

## API reference

Base path: `/api`. All bodies are JSON.

### `GET /api/health`

Returns `{ "status": "ok" }`.

### `POST /api/register`

Request:

```json
{ "username": "alice", "email": "alice@example.com", "password": "supersecret" }
```

Response `200`:

```json
{ "token": "<jwt>", "user": { "id": 1, "username": "alice", "email": "alice@example.com", "created_at": "...", "updated_at": "..." } }
```

Errors: `400` (validation / password too short), `409` (username or email taken).

### `POST /api/login`

Request:

```json
{ "username": "alice", "password": "supersecret" }
```

Response `200`: same shape as register. Errors: `401` (invalid credentials).

### `GET /api/me` (protected)

Header: `Authorization: Bearer <jwt>`. Returns the current `user`. Errors: `401`.

### Quick curl test

```bash
# Login as the seeded test user (via the nginx proxy)
TOKEN=$(curl -s localhost:8081/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"testuser","password":"changeme123"}' | jq -r .token)

curl -s localhost:8081/api/me -H "Authorization: Bearer $TOKEN" | jq
```

## Adding a database migration

1. Create `database/migrations/00X_description.sql`. Make it idempotent
   (`CREATE TABLE IF NOT EXISTS`, `ON CONFLICT`, etc.).
2. Apply it: `docker compose run --rm seed` (or recreate the db volume for a
   clean first-boot init).

> Note: there is no migration-version tracking table yet. Idempotent files keep
> repeated application safe. Introduce a tool like `golang-migrate` if/when
> ordered, irreversible migrations are needed.
