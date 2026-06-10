# Architecture

## Overview

Core Team Builder is a three-tier application:

```
            ┌─────────────────────────────────────────────┐
            │                  Browser                     │
            │   login.html / index.html + js/*.js          │
            └───────────────┬─────────────────────────────┘
                            │ HTTP (same-origin)
                            ▼
            ┌─────────────────────────────────────────────┐
            │  frontend (nginx)                            │
            │   • serves static assets                     │
            │   • proxies /api/* → backend:8080            │
            └───────────────┬─────────────────────────────┘
                            │ HTTP
                            ▼
            ┌─────────────────────────────────────────────┐
            │  backend (Go, net/http)                      │
            │   • /api/register, /api/login (public)       │
            │   • /api/me (JWT-protected)                   │
            │   • bcrypt password hashing, JWT issuance     │
            └───────────────┬─────────────────────────────┘
                            │ pgx connection pool
                            ▼
            ┌─────────────────────────────────────────────┐
            │  db (PostgreSQL 16)                           │
            │   • users table                              │
            └─────────────────────────────────────────────┘
```

## Components

### Frontend (`frontend/`)

Plain static files — no build step. nginx serves them and reverse-proxies
`/api/*` to the backend, keeping API calls same-origin (so the browser does not
hit CORS). The JS is split into:

- `js/api.js` — fetch wrapper, token storage, typed-ish endpoint helpers, plus
  shared constants (roles, classes, skill lines, masteries).
- `js/data.js` — ESO master/seed data for encounters (boss names grouped by
  trial, gear sets with tooltips, skills) and lookup helpers.
- `js/components.js` — reusable, framework-free UI components
  (`createSearchableSelect`: a search box with optional group headers, used by
  the loadout gear/skills pickers).
- `js/auth.js` — login/register page logic.
- `js/app.js` — dashboard logic + route guard (teams, sharing, encounters).

### Backend (`backend/`)

A small Go service using the standard library router (`http.ServeMux` with
method-aware patterns, Go 1.22+). Layout:

- `cmd/server` — HTTP server entrypoint with graceful shutdown.
- `cmd/seed` — one-shot migration + test-user seeder.
- `internal/config` — environment configuration (12-factor).
- `internal/db` — pgx pool with startup retry.
- `internal/models` — domain types + data access (`UserStore`).
- `internal/auth` — bcrypt hashing, JWT issuing/parsing, auth middleware.
- `internal/handlers` — HTTP handlers, JSON helpers, CORS.

### Database (`database/`)

PostgreSQL. The schema in `migrations/*.sql` (e.g. `001_init.sql`,
`002_teams.sql`) is applied two ways:

1. Baked into the image's `/docker-entrypoint-initdb.d/`, so a fresh volume is
   initialized automatically on first boot.
2. By the `seed` command at runtime (idempotent), which then ensures the test
   user exists.

## Data model

### `users`

| column         | type          | notes                              |
|----------------|---------------|------------------------------------|
| id             | bigint (IDENTITY) | primary key                    |
| username       | varchar(50)   | unique, not null                   |
| email          | varchar(255)  | unique, not null                   |
| password_hash  | text          | bcrypt hash, not null              |
| created_at     | timestamptz   | default `now()`                    |
| updated_at     | timestamptz   | auto-updated via trigger           |

### `teams`

| column     | type              | notes                          |
|------------|-------------------|--------------------------------|
| id         | bigint (IDENTITY) | primary key                    |
| name       | varchar(100)      | not null                       |
| owner_id   | bigint            | FK → `users(id)`, cascade      |
| created_at | timestamptz       | default `now()`                |
| updated_at | timestamptz       | auto-updated via trigger       |

### `team_members` (sharing)

| column   | type        | notes                                       |
|----------|-------------|---------------------------------------------|
| team_id  | bigint      | FK → `teams(id)`, cascade; PK part          |
| user_id  | bigint      | FK → `users(id)`, cascade; PK part          |
| role     | varchar(20) | `owner` or `member`                         |
| added_at | timestamptz | default `now()`                             |

The owner is stored here as a `role = 'owner'` row, so any access check is a
single membership lookup.

### `players`

| column         | type         | notes                                    |
|----------------|--------------|------------------------------------------|
| id             | bigint (IDENTITY) | primary key                         |
| team_id        | bigint       | FK → `teams(id)`, cascade                |
| slot           | smallint     | 1–12, unique per team                    |
| name           | varchar(100) | default `''`                             |
| discord_handle | varchar(100) | default `''`                             |
| role           | varchar(20)  | `''`/`tank`/`healer`/`dps`/`support_dps` |
| class          | varchar(30)  | `''` or current ESO class                |
| subclassed     | boolean      | default `false`                          |
| skill_line_1..3| varchar(40)  | `''` or one of 21 class skill lines      |
| mastery_1..2   | varchar(40)  | `''` or one of the class's 5 masteries   |
| created_at     | timestamptz  | default `now()`                          |
| updated_at     | timestamptz  | auto-updated via trigger                 |

Every team is created with all 12 player slots pre-populated (in a single
transaction), so slots are edited rather than added/removed. New slots default
their roles to 2 tanks / 2 healers / 8 dps. Role, class, skill-line, and mastery
values are validated against allow-lists in the backend
(`internal/models/team.go`). Subclassing (`006_player_subclass.sql`) is
mutually exclusive: when `subclassed` is true the three `skill_line_*` columns
apply and the masteries are blanked; when false the two `mastery_*` columns
apply (drawn from the player's class) and the skill lines are blanked.

### `encounters` + `encounter_loadouts`

`encounters` (`007_encounters.sql`) holds per-team named fights:

| column     | type              | notes                                 |
|------------|-------------------|---------------------------------------|
| id         | bigint (IDENTITY) | primary key                           |
| team_id    | bigint            | FK → `teams(id)`, cascade             |
| name       | varchar(100)      | `Default`, `Trash`, or a trial boss   |
| position   | int               | ordering within the team              |
| created_at | timestamptz       | default `now()`                       |
| updated_at | timestamptz       | auto-updated via trigger              |

`encounter_loadouts` holds one loadout per player slot per encounter:

| column       | type     | notes                                       |
|--------------|----------|---------------------------------------------|
| encounter_id | bigint   | FK → `encounters(id)`, cascade; PK part     |
| slot         | smallint | 1–12; PK part                               |
| gear         | text[]   | ordered list of gear-set keys (default `{}`)|
| skills       | text[]   | ordered list of skill keys (default `{}`)   |

Every team has at least one encounter (`Default`), created with the team and
backfilled for existing teams. Encounter names are validated against
`ValidEncounterNames`; gear/skill items are free-form (sanitized, not
allow-listed) — the searchable options + gear tooltips live in the frontend
master data (`frontend/js/data.js`).

## Authentication & security

- **Hashing**: bcrypt (cost 12) with per-hash random salt. Minimum password
  length 8. Plaintext is never stored or logged.
- **Tokens**: HS256-signed JWT containing the user ID (`sub`) and username, with
  an expiry (`JWT_TTL`, default 24h). Stateless — there is no server-side
  session store yet.
- **Login hardening**: a constant-time bcrypt comparison runs even when the
  username does not exist, and all failures return the same generic message, to
  reduce user enumeration and timing side channels.
- **Transport**: terminate TLS at a proxy/load balancer in production. Tokens in
  `localStorage` are acceptable for this tool; revisit if XSS surface grows.

## Deployment model

`docker-compose.yml` defines four services: `db`, `backend`, `seed` (one-shot),
and `frontend`. The backend waits for the database healthcheck before starting.
Only the frontend publishes a host port; backend and db are reachable on the
internal compose network.
