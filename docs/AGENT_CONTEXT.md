# Agent Context — read this first

This file exists so a future AI session (or new contributor) can get up to speed
quickly. Keep it current when the architecture changes.

## What this project is

`core-team-builder` helps design and organize a **trial core team** for *The
Elder Scrolls Online*. It provides accounts + login and **teams**: a user can
own multiple teams and share them with others (viewer/editor roles). Each team
has a trial schedule (days/time/timezone) and a fixed 12-player roster — name,
discord handle, role, ESO class, and a per-player build (either a subclassed set
of 3 skill lines or 2 class masteries). Teams also have **encounters** (Default,
Trash, or a trial boss), each holding a per-player gear/skills loadout. The UI
autosaves changes (no Save buttons).

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
- **Players**: each slot has `name`, `discord_handle`, `role`, `class`, plus a
  subclassing build (`006_player_subclass.sql`). Empty fields = unset. Roles and
  classes are validated against allow-lists in
  `backend/internal/models/eso.go` (`ValidRoles`, `ValidClasses`) — the ESO game
  reference data + build validators live in `eso.go`, separate from the team
  persistence layer in `team.go`:
  - roles: `tank`, `healer`, `dps`, `support_dps` (plus `""`).
  - classes: `arcanist`, `dragonknight`, `necromancer`, `nightblade`,
    `sorcerer`, `templar`, `warden` (plus `""`). The frontend mirrors these
    plus display labels in `frontend/js/data.js` (`ROLES`, `CLASSES`).
  - New teams default the 12 roles to 2 tanks, 2 healers, 8 dps
    (`defaultPlayerRole` in `team.go`).
- **Subclassing** (`006_player_subclass.sql`): each player has `subclassed`
  (bool) plus two mutually exclusive build sets:
  - `subclassed = true` → `skill_line_1..3`, each one of the **21 class skill
    lines** (3 per class). Validated via `ValidSkillLines` / `ValidSkillLine`,
    plus `ValidateSkillLines(class, lines)`: lines must be unique; if the class
    is set and ≥1 line is chosen, ≥1 line must be from that class (fully-empty
    subclass builds are allowed); and ≤1 line from any other class
    (`SkillLineClass` maps line→class). The UI mirrors this via `skillLineClass`
    + `validateBuilds` before each autosave.
  - `subclassed = false` → `mastery_1..2`, drawn from the **5 class masteries of
    the player's class** (`MasteriesByClass` / `ValidMastery(class, m)`).
  - The backend validates only the active set and **blanks the inactive set** on
    save, so the two never coexist. The UI (`frontend/js/data.js`) mirrors these
    as `SKILL_LINE_GROUPS`/`SKILL_LINES` and `MASTERIES_BY_CLASS`/`MASTERIES`,
    and the roster shows a "Subclassed" checkbox that swaps between 3 skill-line
    dropdowns and 2 class-mastery dropdowns (mastery options follow the class).
- **Endpoints** (all JWT-protected): `GET/POST /api/teams`,
  `GET/PUT/DELETE /api/teams/{id}`, `POST /api/teams/{id}/share`,
  `DELETE /api/teams/{id}/members/{userID}`. Mutations return the full refreshed
  team.
- **Save-all**: `PUT /api/teams/{id}` is the single "save everything" call —
  body is `{ name, schedule_days, schedule_time, schedule_timezone, players: [{slot,name,discord_handle,role,class,subclassed,skill_line_1..3,mastery_1..2}] }`
  and the backend (`TeamStore.Save`) updates team meta + roster in one
  transaction (there is no per-player save endpoint). The timezone field defaults
  in the UI to the viewer's current zone
  (`Intl.DateTimeFormat().resolvedOptions().timeZone`).
- **Autosave (UI)**: there are no Save buttons. Changes are persisted
  automatically and debounced/coalesced (~700ms) via `scheduleAutosave` in
  `app.js`: text inputs save on `change` (blur — "input finished"), while
  selects/checkboxes/toggles and loadout chips save immediately. Autosaves do
  **not** re-render the view (so focus / in-progress edits are preserved); a small
  inline `save-status` shows "Saving…/Saved", errors use a toast, and Ctrl/Cmd+S
  forces an immediate save.

## Encounters model (current)

- **Tables** (`007_encounters.sql`): `encounters` (per-team named fights, with
  `position` for ordering) and `encounter_loadouts` (one row per
  `(encounter_id, slot 1–12)` holding `gear TEXT[]` and `skills TEXT[]` —
  ordered, free-form lists of master-data keys). Both cascade on team/encounter
  delete. Every team always has **at least one** encounter named `Default`:
  `TeamStore.Create` creates it for new teams (`createDefaultEncounterTx`), and
  the migration backfills existing teams.
- **Names**: an encounter's name must be in `models.ValidEncounterNames` —
  `Default`, `Trash`, or any ESO trial boss. The frontend mirrors this grouped
  by trial in `frontend/js/data.js` (`ENCOUNTER_NAME_GROUPS`). This is **seed**
  data meant to grow; keep the Go set and the JS groups in sync.
- **Loadout items** (gear sets, skills): stored as keys; the backend does **not**
  validate them against a master list (free-form, defensively sanitized via
  `SanitizeLoadoutItems`: trimmed, non-empty, ≤100 chars, ≤30 items). The
  searchable dropdowns, labels, and gear tooltips live entirely in the frontend
  master data (`GEAR_SETS`, and `SKILL_GROUPS` — skills grouped by skill line,
  with a flat `SKILLS` derived from it — in `data.js`); unknown keys fall back to
  the raw value. Both pickers use the in-house `createSearchableSelect`
  component (`js/components.js`) — a dropdown with full free-text search **and**
  group headers. Skills supply one header per skill line; gear is a single
  headerless group. Expand the seed there.
- **Access/permissions**: mirror the roster — any role can read; editors/owner
  can add, rename, delete, and edit loadouts; viewers are read-only. A team
  cannot delete its **last** encounter.
- **Endpoints** (all JWT-protected, nested under a team):
  `GET/POST /api/teams/{id}/encounters`,
  `GET/PUT/DELETE /api/teams/{id}/encounters/{eid}`,
  `PUT /api/teams/{id}/encounters/{eid}/loadouts`. Mutations return the refreshed
  encounter (with its 12 loadouts).
- **UI**: encounters are integrated into the single team detail page (there is
  **no** separate encounter screen). The **Encounters** card holds a bar of
  selectable chips (the active one is highlighted), an `+ Add Encounter` picker,
  and an `#encounter-controls` row (current name, rename dropdown, delete, save
  status). Selecting a chip (`selectEncounter`) loads that encounter's loadouts
  and refreshes the per-player gear/skill chips inline in the roster — each
  roster slot renders a `[data-loadout]` block (Gear + Skills searchable lists)
  below its subclass/class-mastery section (`renderRosterLoadouts`). Loadouts
  autosave on chip add/remove (or Ctrl/Cmd+S); `selectEncounter` flushes any
  pending loadout autosave before switching so unsaved edits are never dropped.
  See **Autosave (UI)** above.

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
  (see `docs/STYLE_GUIDE.md`). Keep concerns separated: network calls/endpoint
  helpers go in `js/api.js`; shared reference data + display helpers go in
  `js/data.js`; reusable widgets go in `js/components.js`.
- **Config**: env vars are read in `backend/internal/config/config.go` and wired
  through `docker-compose.yml` + `.env`.

## Conventions

- Migrations are **idempotent** (`IF NOT EXISTS`, `ON CONFLICT`) so both the
  Postgres init dir and the `seed` command can apply them safely.
- The `seed` binary applies all `*.sql` in `MIGRATIONS_DIR` (sorted) then
  upserts the test user. It is safe to run repeatedly. The test-user
  credentials (`SEED_USERNAME`, `SEED_EMAIL`, `SEED_PASSWORD`) are **required
  from the environment** — there are no hardcoded defaults — and the plaintext
  password is never logged.
- Secrets/credentials come from the environment only; never hardcode. `.env` is
  git-ignored.

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
- [x] Encounters: per-team named fights with per-player gear/skill loadouts.
- [ ] Expand the gear-set/skill/boss seed data to full ESO coverage.
- [ ] Tests (handlers, auth, models).
- [ ] Rate limiting on auth endpoints.
