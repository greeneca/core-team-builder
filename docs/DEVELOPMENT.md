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
| `SEED_USERNAME`   | seed      | **Required.** Test user username                   |
| `SEED_EMAIL`      | seed      | **Required.** Test user email                      |
| `SEED_PASSWORD`   | seed      | **Required.** Test user password                   |

Seed credentials have no hardcoded defaults — they must be supplied via the
environment so they stay isolated in `.env`. The seeder never logs the plaintext
password.

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

# Seed credentials are required (no defaults):
export SEED_USERNAME="testuser"
export SEED_EMAIL="test@example.com"
export SEED_PASSWORD="changeme123"

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

### Teams (all protected)

All routes require `Authorization: Bearer <jwt>`. A team is accessible to its
owner and any user it has been shared with; inaccessible teams return `404`.
Mutating endpoints return the full refreshed team (with `players` and `members`).

| Method & path                              | Who      | Description                          |
|--------------------------------------------|----------|--------------------------------------|
| `GET /api/teams`                           | member   | List teams you own or that are shared with you (`{ "teams": [...] }`). |
| `POST /api/teams`                          | any user | Create a team `{ "name": "...", "copy_from"?: <teamID> }` → `201`. Without `copy_from`: 12 empty slots + a `Default` encounter. With `copy_from` (a team you can access): copies its schedule, roster, and encounters/loadouts (never its sharing). |
| `GET /api/teams/{id}`                      | viewer+  | Get one team with `players` + `members`. |
| `PUT /api/teams/{id}`                      | editor+  | Save everything: `{ name, schedule_days, schedule_time, team_timezones, players }`. |
| `DELETE /api/teams/{id}`                   | owner    | Delete the team (cascades).          |
| `POST /api/teams/{id}/share`              | owner    | Share/update role `{ "username": "...", "role": "viewer"\|"editor" }` (role defaults to `editor`; upsert). |
| `DELETE /api/teams/{id}/members/{userID}` | owner    | Revoke a member's access.            |

Roles: `owner` and `editor` can edit the team/roster; `viewer` is read-only
(edit attempts return `403`). Only the owner can delete or manage sharing.

`PUT /api/teams/{id}` body (the UI autosaves all of this together; see Autosave
in `docs/AGENT_CONTEXT.md`):

```json
{
  "name": "Sunday Trial Core",
  "schedule_days": ["mon", "wed"],
  "schedule_time": "00:00",
  "team_timezones": ["America/New_York", "Europe/London"],
  "players": [
    {
      "slot": 1, "name": "Aedric", "discord_handle": "aedric#1234",
      "role": "tank", "class": "dragonknight",
      "subclassed": true,
      "skill_line_1": "ardent_flame", "skill_line_2": "draconic_power", "skill_line_3": "grave_lord",
      "mastery_1": "", "mastery_2": ""
    }
  ]
}
```

- `schedule_days` ⊆ `mon,tue,wed,thu,fri,sat,sun` (validated, de-duped, ordered).
- `schedule_time` is `""` or `"HH:MM"` (24h) **in UTC**; anything else returns
  `400`. There is no manual timezone picker — the UI converts the time from the
  viewer's current zone to UTC before saving and back to the viewer's zone on
  load, so each viewer sees the time in their own zone.
- `team_timezones` is an optional list of IANA names — extra zones the team wants
  the time shown in. Validated + de-duped via `normalizeTimezones`
  (`time.LoadLocation`); invalid names return `400`. Managed as removable chips
  plus a searchable add-picker on the team page (default `[]`).
- `players` is optional; omitted slots are left unchanged. Invalid slot/role/
  class/skill-line/mastery returns `400` and the whole save is rolled back.

Player slot body:

```json
{
  "name": "Aedric", "discord_handle": "aedric#1234", "role": "tank", "class": "dragonknight",
  "subclassed": false, "mastery_1": "booming_voice", "mastery_2": "rousing_roar"
}
```

- `role` ∈ `""`, `tank`, `healer`, `dps`, `support_dps`.
- `class` ∈ `""`, `arcanist`, `dragonknight`, `necromancer`, `nightblade`,
  `sorcerer`, `templar`, `warden`.
- `subclassed` (bool) selects which build set applies:
  - `true` → `skill_line_1..3`, each one of the 21 class skill lines (or `""`).
  - `false` → `mastery_1..2`, each one of the **selected class's** 5 masteries
    (or `""`).
  - The backend validates only the active set and blanks the inactive one, so a
    player never has both skill lines and masteries stored.
- Subclass skill-line rules (`models.ValidateSkillLines`, enforced on save):
  - selected skill lines must be **unique**;
  - if `class` is set **and at least one line is chosen**, **at least one**
    selected line must be from that class (a fully-empty subclass build is
    allowed);
  - if `class` is set, **at most one** selected line may come from any single
    other class. Class checks are skipped when `class` is `""`. Violations
    return `400` (the UI pre-checks and names the offending slot).

Invalid `role`/`class`/`skill_line_*`/`mastery_*` values return `400`.

### Encounters (all protected)

Nested under a team; the same access rules apply (viewer reads; editor/owner
edits). Mutations return the refreshed encounter with its 12 loadouts.

| Method & path                                          | Who     | Description                                |
|--------------------------------------------------------|---------|--------------------------------------------|
| `GET /api/teams/{id}/encounters`                       | viewer+ | List a team's encounters (`{ "encounters": [...] }`, no loadouts). |
| `POST /api/teams/{id}/encounters`                      | editor+ | Add an encounter `{ "name": "...", "copy_from"?: <encounterID> }` → `201`; creates 12 loadouts (empty, or copied slot-for-slot from `copy_from` in the same team). |
| `GET /api/teams/{id}/encounters/{eid}`                 | viewer+ | Get one encounter with its 12 `loadouts`.  |
| `PUT /api/teams/{id}/encounters/{eid}`                 | editor+ | Rename `{ "name": "..." }`.                |
| `DELETE /api/teams/{id}/encounters/{eid}`              | editor+ | Delete (cannot delete the team's last one).|
| `PUT /api/teams/{id}/encounters/{eid}/loadouts`        | editor+ | Save loadouts (see body below).            |

Every team always has at least one encounter (`Default`), created with the team.
`name` must be `Default`, `Trash`, or an ESO trial boss (`ValidEncounterNames`).
On create/rename the name is also checked by `ValidateEncounterSelection`: names
are **unique** per team, and all non-`General` encounters must come from a
**single trial** (the `General` group — `Default`/`Trash` — is always allowed
alongside one trial). Violations return `400`.

`PUT .../loadouts` body:

```json
{
  "loadouts": [
    { "slot": 1, "gear": ["perfected_relequen", "slimecraw"], "skills": ["pragmatic_fatecarver"] }
  ]
}
```

- `gear`/`skills` are ordered, free-form key lists (the UI constrains choices to
  the master data in `frontend/js/data.js`). The backend sanitizes them (trim,
  drop empties, ≤100 chars each, ≤30 items) but does not allow-list the keys.
- `slot` must be 1–12; an invalid slot or item returns `400`.

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
