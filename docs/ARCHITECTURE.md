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
            │   • /api/me, /api/teams, .../encounters       │
            │     (JWT-protected)                          │
            │   • bcrypt password hashing, JWT issuance     │
            └───────────────┬─────────────────────────────┘
                            │ pgx connection pool
                            ▼
            ┌─────────────────────────────────────────────┐
            │  db (PostgreSQL 16)                           │
            │   • users, teams, team_members, players,      │
            │     encounters, encounter_loadouts            │
            └─────────────────────────────────────────────┘
```

## Components

### Frontend (`frontend/`)

Plain static files — no build step. nginx serves them and reverse-proxies
`/api/*` to the backend, keeping API calls same-origin (so the browser does not
hit CORS). The JS is split into:

- `js/api.js` — fetch wrapper, token storage, and endpoint helpers (the `api`
  client object only — no domain data).
- `js/data.js` — all shared reference data + display helpers: roles, classes,
  races, share roles, days, timezone/schedule helpers, subclassing skill lines
  and class masteries, and the ESO encounter master/seed data (boss names grouped
  by trial, gear sets with tooltips, skills, potions, mundus stones, weapon lines,
  blue CP) with lookup helpers. Also holds the `BUFFS` list +
  `computeBuffCoverage()`, the crit model (`CRIT_*` source tables +
  `computeCritCoverage()`), and the penetration model (`PEN_*` source tables +
  `computePenCoverage()`). Buffs, crit, and penetration are frontend-only
  reference data; coverage is computed from the roster build + the selected
  encounter's loadout — see `docs/AGENT_CONTEXT.md` "Buffs model", "Crit damage
  model", and "Penetration model".
- `js/components.js` — reusable, framework-free UI components
  (`createSearchableSelect`: a search box with optional group headers, used by
  the loadout gear/skills pickers) plus `initTooltips`: an app-wide floating
  tooltip driven by `data-tip` attributes (gear set + skill descriptions, and
  the encounters `.info-indicator` help badges) on both
  the picker options and the selected chips). Tooltips can be toggled off from
  the topbar; the preference persists in `localStorage`.
- `js/auth.js` — login/register page logic.
- `js/app.js` — dashboard logic + route guard (teams, sharing, encounters).

### Backend (`backend/`)

A small Go service using the standard library router (`http.ServeMux` with
method-aware patterns, Go 1.22+). Layout:

- `cmd/server` — HTTP server entrypoint with graceful shutdown.
- `cmd/seed` — one-shot migration + test-user seeder.
- `internal/config` — environment configuration (12-factor).
- `internal/db` — pgx pool with startup retry.
- `internal/models` — domain types + data access (`UserStore`, `TeamStore`,
  `EncounterStore`). ESO game reference data and the player-build validators live
  in `eso.go`, kept separate from the persistence stores.
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

| column         | type              | notes                                      |
|----------------|-------------------|--------------------------------------------|
| id             | bigint (IDENTITY) | primary key                                |
| name           | varchar(100)      | not null                                   |
| owner_id       | bigint            | FK → `users(id)`, cascade                  |
| schedule_days  | text[]            | weekday keys, e.g. `{mon,wed}` (default `{}`) |
| schedule_time  | varchar(5)        | `"HH:MM"` 24h **in UTC**, `''` when unset  |
| team_timezones | text[]            | extra IANA zones to display the time in (default `{}`) |
| created_at     | timestamptz       | default `now()`                            |
| updated_at     | timestamptz       | auto-updated via trigger                   |

The trial schedule lives on `teams` (`004_team_schedule.sql`,
`009_team_timezones.sql`). `schedule_time` is stored in UTC and converted to/from
each viewer's current timezone in the browser; the old per-team
`schedule_timezone` column was removed in `010_drop_schedule_timezone.sql`.

### `team_members` (sharing)

| column   | type        | notes                                       |
|----------|-------------|---------------------------------------------|
| team_id  | bigint      | FK → `teams(id)`, cascade; PK part          |
| user_id  | bigint      | FK → `users(id)`, cascade; PK part          |
| role     | varchar(20) | `owner`, `editor`, or `viewer`              |
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
| race           | text         | `''` or playable race; `013_player_race.sql` |
| subclassed     | boolean      | default `false`                          |
| skill_line_1..3| varchar(40)  | `''` or one of 21 class skill lines      |
| mastery_1..2   | varchar(40)  | `''` or one of the class's 5 masteries   |
| created_at     | timestamptz  | default `now()`                          |
| updated_at     | timestamptz  | auto-updated via trigger                 |

Every team is created with all 12 player slots pre-populated (in a single
transaction), so slots are edited rather than added/removed. New slots default
their roles to 2 tanks / 2 healers / 8 dps. Role, class, skill-line, and mastery
values are validated against allow-lists in the backend
(`internal/models/eso.go`). Subclassing (`006_player_subclass.sql`) is
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
| potions      | text[]   | ordered list of potion keys (default `{}`); `011_loadout_potions.sql` |
| cp_blue      | text[]   | slotted blue (Warfare) CP star keys (default `{}`); `012_loadout_crit.sql` |
| weapons      | text[]   | equipped weapon-line keys (default `{}`); `012_loadout_crit.sql` |
| mundus       | text     | mundus stone key (default `''`); `012_loadout_crit.sql` |
| armor_heavy/medium/light | smallint | armor-piece counts 0–7 (default `0`); `012_loadout_crit.sql` |
| pen_extra    | text[]   | flat penetration source keys (default `{}`); `014_loadout_pen_extra.sql` |

Every team has at least one encounter (`Default`), created with the team and
backfilled for existing teams. Encounter names are validated against
`ValidEncounterNames` and must be **unique per team** and all from a **single
trial** (plus the always-allowed `General` group) — see
`ValidateEncounterSelection`, with a unique index on `encounters(team_id, name)`
as a DB backstop. gear/skill/potion/cp_blue/weapons/pen_extra items are free-form
(sanitized, not allow-listed); mundus is a trimmed string and the armor counts
are clamped to 0–7 — the searchable options + tooltips live in the frontend
master data (`frontend/js/data.js`). The crit-input columns
(`cp_blue`/`weapons`/`mundus`/armor) feed the client-side crit-damage calculator
(see `docs/AGENT_CONTEXT.md` "Crit damage model"); `pen_extra` plus those reused
inputs feed the penetration calculator (see "Penetration model"); `players.race`
(`013_player_race.sql`) is the roster-level input both also read.

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
All services are attached to a single user-defined bridge network (`ctb-net`),
which isolates the project from other compose stacks and lets the services reach
each other by service name (e.g. `db:5432`, `backend:8080`). Only the frontend
publishes a host port; `db` and `backend` are reachable on `ctb-net` only.
