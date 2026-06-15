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
            │   • /api/register, /api/login, /api/refresh,  │
            │     /api/logout, /api/forgot-password,        │
            │     /api/reset-password,                      │
            │     /api/registration-status (public)        │
            │   • /api/me, /api/teams, .../encounters,      │
            │     .../groupings, /api/admin/* (JWT)         │
            │   • bcrypt hashing, access JWT + refresh tokens│
            │   • SMTP email (password resets)             │
            └───────────────┬─────────────────────────────┘
                            │ pgx connection pool
                            ▼
            ┌─────────────────────────────────────────────┐
            │  db (PostgreSQL 16)                           │
            │   • users, app_settings, refresh_tokens,      │
            │     password_resets, teams, team_members,     │
            │     players, encounters, encounter_loadouts,  │
            │     groupings, grouping_groups,               │
            │     grouping_members, discord_link_codes,     │
            │     discord_channels                          │
            └───────────────▲─────────────────────────────┘
                            │ pgx (shares stores)
            ┌───────────────┴─────────────────────────────┐
            │  bot (Go, discordgo) — optional, no inbound  │
            │   • /coreteam slash command (link/setup/     │
            │     post/status/unset) + Get my details DM    │
            │   • outbound WebSocket to the Discord gateway │
            └─────────────────────────────────────────────┘
```

## Components

### Frontend (`frontend/`)

Plain static files — no build step. nginx serves them and reverse-proxies
`/api/*` to the backend, keeping API calls same-origin (so the browser does not
hit CORS). The JS is split into:

- `js/api.js` — fetch wrapper, token storage, and endpoint helpers (the `api`
  client object only — no domain data).
- `js/gear-skills.js` — the ESO gear-set and skill (ability) catalogs
  (`GEAR_SET_GROUPS` / `GEAR_SETS`, `SKILL_GROUPS` / `SKILLS`, and
  `GEAR_ABBREVIATIONS`), split out from `data.js` so these frequently-updated
  tables are easy to edit in isolation. **Must load before `data.js`** (the
  lookups/helpers there consume these globals).
- `js/data.js` — the rest of the shared reference data + display helpers: roles,
  classes, races, share roles, days, timezone/schedule helpers, subclassing skill
  lines and class masteries, and the remaining ESO encounter master/seed data
  (boss names grouped by trial, potions, mundus stones, weapon lines, blue CP)
  plus the gear/skill lookup helpers (which consume the catalogs from
  `gear-skills.js`). Also holds the `BUFFS` list + `computeBuffCoverage()`, the
  crit model (`CRIT_*` source tables + `computeCritCoverage()`), and the
  penetration model (`PEN_*` source tables + `computePenCoverage()`). Buffs,
  crit, and penetration are frontend-only reference data; coverage is computed
  from the roster build + the selected encounter's loadout — see
  `docs/AGENT_CONTEXT.md` "Buffs model", "Crit damage model", and "Penetration
  model".
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
- `cmd/bot` — the Discord bot (`discordgo`): registers the `/coreteam` slash
  command and handles its component/modal interactions. Shares the same stores
  and DB; opens only an outbound gateway connection (no inbound port). Run via
  the `bot` compose profile. See `docs/AGENT_CONTEXT.md` "Discord bot".
- `internal/config` — environment configuration (12-factor).
- `internal/db` — pgx pool with startup retry.
- `internal/models` — domain types + data access (`UserStore`, `TeamStore`,
  `EncounterStore`, `GroupingStore`, `RefreshTokenStore`, `PasswordResetStore`,
  `DiscordStore` (link codes + channel bindings), and `SettingsStore` for the
  `app_settings` key/value store). ESO game reference data and the player-build
  validators live in `eso.go`, kept separate from the persistence stores.
- `internal/esoref` — code-generated ESO labels/abbreviations
  (`data_gen.go`, produced by `tools/gen-esoref/gen.js` from the frontend's
  `gear-skills.js`/`data.js`) so the bot renders the same names as the web UI.
- `internal/discordfmt` — Go ports of the frontend Discord formatters
  (condensed overview + per-player detail) used by the bot.
- `internal/auth` — bcrypt hashing, access-JWT issuing/parsing, opaque-token
  generation (refresh + reset), auth middleware.
- `internal/email` — outbound mail abstraction (`Mailer`): an `SMTPMailer`
  (implicit TLS on 465, STARTTLS otherwise) when SMTP is configured, else a
  `LogMailer` that logs messages for local development.
- `internal/handlers` — HTTP handlers, JSON helpers, CORS. The `Server` is
  constructed from a `handlers.Config` struct. Split by area: `handlers.go` (the
  `Server`, routing, auth/refresh, shared helpers), `teams.go`, `encounters.go`,
  `groupings.go`, `password_reset.go` (forgot/reset password), and admin-only
  user/settings handlers (with a `requireAdmin` gate) in `admin.go`.

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
| is_admin       | boolean       | default `false`; `015_admin_and_settings.sql` |
| discord_user_id  | text        | linked Discord user ID, unique when set; `027_discord.sql` |
| discord_username | text        | linked Discord display name (default `''`); `027_discord.sql` |
| created_at     | timestamptz   | default `now()`                    |
| updated_at     | timestamptz   | auto-updated via trigger           |

Admins manage users and toggle registration. The first account ever registered
becomes an admin (and is always allowed); the seeded test user is always an
admin. See `docs/AGENT_CONTEXT.md` "Admin & user management".

### `app_settings`

A simple key/value store for global configuration (`015_admin_and_settings.sql`).

| column | type | notes                                            |
|--------|------|--------------------------------------------------|
| key    | text | primary key                                      |
| value  | text | not null                                         |

Currently holds `registration_enabled` (`'true'`/`'false'`, default `'true'`),
which gates public self-registration. Read/written via `SettingsStore`.

### `refresh_tokens`

Backs the access-token / refresh-token scheme (`016_refresh_tokens.sql`). Access
tokens are stateless JWTs; refresh tokens are opaque random strings stored here
**only as a SHA-256 hash** so a DB read yields no usable credential.

| column     | type        | notes                                          |
|------------|-------------|------------------------------------------------|
| id         | bigint (IDENTITY) | primary key                              |
| user_id    | bigint      | FK → `users(id)`, cascade                      |
| token_hash | text        | hex SHA-256 of the token, unique               |
| expires_at | timestamptz | refresh-token expiry (`REFRESH_TTL`)           |
| revoked_at | timestamptz | set on rotation/logout (NULL while active)     |
| created_at | timestamptz | default `now()`                                |

Rows support rotation (`/api/refresh`) and explicit revocation (`/api/logout`);
a background hourly sweep prunes expired rows. Managed via `RefreshTokenStore`.

### `password_resets`

Backs the forgot/reset-password flow (`021_password_resets.sql`). A reset token
is an opaque random string emailed to the user; only its SHA-256 hash is stored
here, so a DB read yields no usable token. Tokens are single-use and
time-limited.

| column     | type        | notes                                          |
|------------|-------------|------------------------------------------------|
| id         | bigint (IDENTITY) | primary key                              |
| user_id    | bigint      | FK → `users(id)`, cascade                      |
| token_hash | text        | hex SHA-256 of the token, unique               |
| expires_at | timestamptz | reset-token expiry (`PASSWORD_RESET_TTL`)      |
| used_at    | timestamptz | set when consumed (NULL while unused)          |
| created_at | timestamptz | default `now()`                                |

`POST /api/forgot-password` issues a row (after invalidating the user's prior
unused rows); `POST /api/reset-password` consumes it atomically and then revokes
all of the user's refresh tokens. The same hourly sweep prunes expired/used
rows. Managed via `PasswordResetStore`.

### `teams`

| column         | type              | notes                                      |
|----------------|-------------------|--------------------------------------------|
| id             | bigint (IDENTITY) | primary key                                |
| name           | varchar(100)      | not null                                   |
| owner_id       | bigint            | FK → `users(id)`, cascade                  |
| schedule_days  | text[]            | weekday keys, e.g. `{mon,wed}` (default `{}`) |
| schedule_time  | varchar(5)        | `"HH:MM"` 24h **in UTC**, `''` when unset  |
| team_timezones | text[]            | extra IANA zones to display the time in (default `{}`) |
| encounters_enabled | boolean       | surface multi-encounter UI; default `false`; `017_team_encounters_enabled.sql` |
| signup_note    | text              | condensed-list footer (default `''`); `018_team_signup_note.sql` |
| detailed_header| text              | detailed-post header (default `''`); `019_team_detailed_header.sql` |
| created_at     | timestamptz       | default `now()`                            |
| updated_at     | timestamptz       | auto-updated via trigger                   |

The trial schedule lives on `teams` (`004_team_schedule.sql`,
`009_team_timezones.sql`). `schedule_time` is stored in UTC and converted to/from
each viewer's current timezone in the browser; the old per-team
`schedule_timezone` column was removed in `010_drop_schedule_timezone.sql`.
`encounters_enabled` gates the multi-encounter UI (off → only the first encounter
shows); `signup_note` / `detailed_header` are free-form text for the Discord
signup export (see `docs/AGENT_CONTEXT.md` "Discord signup export").

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
| crit_dmg     | text[]   | crit-damage source keys, i.e. crit weapon-line passives (default `{}`); `012_loadout_crit.sql` (added as `weapons`), renamed by `022_loadout_crit_dmg_rename.sql` |
| mundus       | text     | mundus stone key (default `''`); `012_loadout_crit.sql` |
| armor_heavy/medium/light | smallint | armor-piece counts 0–7 (default `0`); `012_loadout_crit.sql` |
| pen_extra    | text[]   | flat penetration source keys (default `{}`); `014_loadout_pen_extra.sql` |
| catalyst_elements | integer | Elemental Catalyst damage types applied 1–3 (default `3`); `023_loadout_catalyst_elements.sql` |
| weapon_damage | integer | higher of Weapon/Spell Damage for Anthelmir's Construct pen (default `0`); `024_loadout_weapon_damage.sql` |
| splintered_secrets_skills | integer | slotted Herald of the Tome abilities 0–2 for Splintered Secrets pen (default `2`); `025_loadout_splintered_secrets_skills.sql` |
| force_of_nature_status | integer | negative status effects on enemy 0–5 for Force of Nature CP pen (default `5`); `026_loadout_force_of_nature_status.sql` |

Every team has at least one encounter (`Default`), created with the team and
backfilled for existing teams. Encounter names are validated against
`ValidEncounterNames` and must be **unique per team** and all from a **single
trial** (plus the always-allowed `General` group) — see
`ValidateEncounterSelection`, with a unique index on `encounters(team_id, name)`
as a DB backstop. gear/skill/potion/cp_blue/crit_dmg/pen_extra items are free-form
(sanitized, not allow-listed); mundus is a trimmed string and the armor counts
are clamped to 0–7 — the searchable options + tooltips live in the frontend
master data (`frontend/js/data.js`). The crit-input columns
(`cp_blue`/`crit_dmg`/`mundus`/armor) feed the client-side crit-damage calculator
(see `docs/AGENT_CONTEXT.md` "Crit damage model"); `pen_extra` plus those reused
inputs feed the penetration calculator (see "Penetration model"); `players.race`
(`013_player_race.sql`) is the roster-level input both also read.

### `groupings` + `grouping_groups` + `grouping_members`

A **grouping** (`020_groupings.sql`) splits a team's roster into numbered groups
for mechanics (e.g. ice cages, slayer stacks). A team may have several; a player
belongs to at most one group per grouping.

`groupings`:

| column      | type              | notes                                |
|-------------|-------------------|--------------------------------------|
| id          | bigint (IDENTITY) | primary key                          |
| team_id     | bigint            | FK → `teams(id)`, cascade            |
| name        | varchar(100)      | default `'Grouping'`                 |
| group_count | int               | 1–12 (CHECK), default `2`            |
| position    | int               | ordering within the team             |
| created_at  | timestamptz       | default `now()`                      |
| updated_at  | timestamptz       | auto-updated via trigger             |

`grouping_groups` holds each numbered group's optional name (PK
`(grouping_id, group_number)`; blank → UI shows "Group N"). `grouping_members`
holds slot assignments with PK `(grouping_id, player_slot)`, which guarantees a
player is in at most one group per grouping. Both cascade on grouping delete.
Managed via `GroupingStore`; see `docs/AGENT_CONTEXT.md` "Groupings model".

### `discord_link_codes` + `discord_channels`

Back the Discord bot (`027_discord.sql`). A **link code** is a short, opaque,
single-use code generated in the web UI to link a Discord identity to an app
account; only its SHA-256 hash is stored (mirrors `password_resets`).

`discord_link_codes`:

| column     | type        | notes                                          |
|------------|-------------|------------------------------------------------|
| id         | bigint (IDENTITY) | primary key                              |
| user_id    | bigint      | FK → `users(id)`, cascade                      |
| code_hash  | text        | hex SHA-256 of the link code, unique           |
| expires_at | timestamptz | code expiry (15 min)                           |
| used_at    | timestamptz | set when consumed (NULL while unused)          |
| created_at | timestamptz | default `now()`                                |

`discord_channels` binds a Discord channel to a team (so `/coreteam post` knows
which roster to render):

| column         | type        | notes                                      |
|----------------|-------------|--------------------------------------------|
| guild_id       | text        | Discord guild (server) ID                  |
| channel_id     | text        | primary key                                |
| team_id        | bigint      | FK → `teams(id)`, cascade                  |
| set_by_user_id | bigint      | FK → `users(id)`, set null on delete       |
| created_at     | timestamptz | default `now()`                            |
| updated_at     | timestamptz | bump on rebind                             |

Managed via `DiscordStore`; the hourly sweep prunes expired/used link codes. See
`docs/AGENT_CONTEXT.md` "Discord bot".

## Authentication & security

- **Hashing**: bcrypt (cost 12) with per-hash random salt. Minimum password
  length 8. Plaintext is never stored or logged.
- **Tokens**: a short-lived HS256-signed **access JWT** containing the user ID
  (`sub`) and username, with an expiry (`JWT_TTL`, default 15m), paired with a
  long-lived opaque **refresh token** (`REFRESH_TTL`, default 30d) stored
  DB-backed as a SHA-256 hash in `refresh_tokens`. `POST /api/refresh` rotates the
  pair; `POST /api/logout` revokes the refresh token. Access tokens stay stateless
  (no per-request DB lookup); revocation/logout is handled via the refresh-token
  table.
- **Password reset**: `/api/forgot-password` always returns a generic response
  (no account enumeration) and emails a single-use, hashed, time-limited token
  (`PASSWORD_RESET_TTL`, default 1h) as a link to `<APP_BASE_URL>/reset.html`.
  `/api/reset-password` enforces the password policy, consumes the token
  atomically (single-use), and revokes all of the user's refresh tokens
  (sign-out-everywhere). Tokens are stored only as a SHA-256 hash in
  `password_resets`; email is sent via SMTP (or logged in dev when SMTP is
  unset). Both endpoints sit behind the nginx `auth` rate-limit zone.
- **Login hardening**: a constant-time bcrypt comparison runs even when the
  username does not exist, and all failures return the same generic message, to
  reduce user enumeration and timing side channels.
- **Authorization**: team access is role-based (owner/editor/viewer) via
  `team_members`; the admin-only `/api/admin/*` routes re-check `users.is_admin`
  server-side (`requireAdmin`) — the frontend gating is convenience only.
  Self-registration can be disabled by an admin (`app_settings`).
- **Transport**: terminate TLS at a proxy/load balancer in production. Tokens in
  `localStorage` are acceptable for this tool; revisit if XSS surface grows.

## Deployment model

`docker-compose.yml` defines five services: `db`, `backend`, `seed` (one-shot),
`frontend`, and `bot` (Discord bot, behind the `bot` compose profile so a plain
`docker compose up` does not start it). The backend waits for the database
healthcheck before starting. All services are attached to a single user-defined
bridge network (`ctb-net`), which isolates the project from other compose stacks
and lets the services reach each other by service name (e.g. `db:5432`,
`backend:8080`). Only the frontend publishes a host port; `db`, `backend`, and
`bot` are reachable on `ctb-net` only (the bot also makes an outbound connection
to the Discord gateway). Start the bot with `docker compose --profile bot up`.
