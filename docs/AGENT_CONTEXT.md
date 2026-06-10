# Agent Context — read this first

This file exists so a future AI session (or new contributor) can get up to speed
quickly. Keep it current when the architecture changes.

## What this project is

`core-team-builder` helps design and organize a **trial core team** for *The
Elder Scrolls Online*. Today it provides accounts + login and **teams**: a user
can own multiple teams, share them with other users, and each team has a fixed
12-player roster (name, discord handle, role, ESO class).

## Stack at a glance

| Layer    | Tech                         | Location     |
|----------|------------------------------|--------------|
| Frontend | static HTML/CSS/JS + nginx   | `frontend/`  |
| Backend  | Go (stdlib `net/http` mux)   | `backend/`   |
| Database | PostgreSQL 16                | `database/`  |
| Orchestr | Docker Compose               | `docker-compose.yml` |

- Go module path: `github.com/core-team-builder/backend` (Go 1.25).
- Key deps: `jackc/pgx/v5` (Postgres), `golang-jwt/jwt/v5` (tokens),
  `golang.org/x/crypto/bcrypt` (passwords).

## How auth works (current)

1. `POST /api/register` or `POST /api/login` → backend verifies/creates user,
   returns a signed JWT (`{ token, user }`).
2. Frontend stores the token in `localStorage` (`ctb_token`) and sends it as
   `Authorization: Bearer <token>` on protected calls.
3. `auth.Middleware` validates the token and injects the user ID into the
   request context. `GET /api/me` is the example protected route.

Passwords: bcrypt cost 12, min length 8. Hashes only ever leave via `password_hash`
column; the `User` JSON model hides it (`json:"-"`).

## Teams model (current)

- **Tables** (`002_teams.sql`): `teams` (owner + name), `team_members`
  (sharing; the owner is a row with role `owner`), `players` (12 rows per team,
  one per `slot` 1–12). `004_team_schedule.sql` + `005_team_timezone.sql` add the
  trial schedule to `teams`: `schedule_days TEXT[]` (e.g. `{mon,wed}`),
  `schedule_time` (`"HH:MM"` 24h, `''` when unset), and `schedule_timezone`
  (IANA name, e.g. `America/New_York`, `''` when unset). Day keys validated
  against `ValidDays`; timezone validated via `time.LoadLocation` (the server
  embeds `time/tzdata` so this works in the Alpine image).
- **Access**: a single `team_members` lookup yields the caller's role —
  `owner`, `editor`, or `viewer`. Owners and editors can rename a team and edit
  players; **viewers are read-only**; **only the owner** can delete the team or
  manage sharing (add/remove members, change a member's role). Inaccessible
  teams return `404` (not `403`) so other users' teams are not revealed; viewers
  attempting an edit get `403`.
- **Sharing roles**: `POST /api/teams/{id}/share` takes `{ username, role }`
  where role is `viewer` or `editor` (defaults to `editor`). It is an upsert, so
  re-sharing changes an existing member's role. Allow-list: `ValidShareRoles`
  in `team.go`; role constants `RoleOwner`/`RoleEditor`/`RoleViewer`. Migration
  `003_share_roles.sql` converts legacy `member` rows to `editor`.
- **Players**: each slot has `name`, `discord_handle`, `role`, `class`. Empty
  fields = unset. Roles and classes are validated against allow-lists in
  `backend/internal/models/team.go` (`ValidRoles`, `ValidClasses`):
  - roles: `tank`, `healer`, `dps`, `support_dps` (plus `""`).
  - classes: `arcanist`, `dragonknight`, `necromancer`, `nightblade`,
    `sorcerer`, `templar`, `warden` (plus `""`). The frontend mirrors these
    plus display labels in `frontend/js/api.js` (`ROLES`, `CLASSES`).
- **Endpoints** (all JWT-protected): `GET/POST /api/teams`,
  `GET/PUT/DELETE /api/teams/{id}`, `POST /api/teams/{id}/share`,
  `DELETE /api/teams/{id}/members/{userID}`. Mutations return the full refreshed
  team.
- **Save-all**: `PUT /api/teams/{id}` is the single "save everything" call —
  body is `{ name, schedule_days, schedule_time, schedule_timezone, players: [{slot,name,discord_handle,role,class}] }`
  and the backend (`TeamStore.Save`) updates team meta + roster in one
  transaction. The UI uses a single **Save All** button (there is no per-player
  save endpoint). The timezone field defaults in the UI to the viewer's current
  zone (`Intl.DateTimeFormat().resolvedOptions().timeZone`).

## Request flow

Browser → nginx (`frontend`, port 80→`FRONTEND_PORT`) → `/api/*` proxied to
`backend:8080` → `db:5432`. Because `/api` is same-origin via the proxy, CORS is
generally not triggered (the backend still sets CORS headers as defense).

## Where to make changes

- **New API endpoint**: add a handler in `backend/internal/handlers/handlers.go`
  and register it in `Routes()`. Protected routes wrap with
  `s.tokens.Middleware(...)`.
- **New table / query**: add migration in `database/migrations/` (idempotent),
  add a store + methods in `backend/internal/models/`.
- **New page / UI**: add an `.html` file in `frontend/`, a script in
  `frontend/js/`, and reuse tokens/classes from `frontend/css/styles.css`
  (see `docs/STYLE_GUIDE.md`).
- **Config**: env vars are read in `backend/internal/config/config.go` and wired
  through `docker-compose.yml` + `.env`.

## Conventions

- Migrations are **idempotent** (`IF NOT EXISTS`, `ON CONFLICT`) so both the
  Postgres init dir and the `seed` command can apply them safely.
- The `seed` binary applies all `*.sql` in `MIGRATIONS_DIR` (sorted) then
  upserts the test user. It is safe to run repeatedly.
- Secrets come from the environment only; never hardcode. `.env` is git-ignored.

## Common commands

```bash
docker compose up --build          # run the whole stack
docker compose run --rm seed       # (re)apply migrations + ensure test user
cd backend && go build ./...       # compile backend
cd backend && go vet ./...         # static checks
```

## Status / TODO ideas

- [ ] Token refresh / logout server-side invalidation (currently stateless JWT).
- [x] Teams: ownership, sharing, and a 12-player roster (name/discord/role/class).
- [ ] Build/set planning per player (gear, skills) on top of the roster.
- [ ] Tests (handlers, auth, models).
- [ ] Rate limiting on auth endpoints.
