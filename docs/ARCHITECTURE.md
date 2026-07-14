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
            │     /api/registration-status,                │
            │     /api/auth/discord/* (OAuth, public)      │
            │   • /api/me, /api/teams, .../encounters,      │
            │     .../groupings, .../roster-members,        │
            │     /api/discord/*, /api/admin/* (JWT)        │
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
            │     grouping_members, team_roster_members,    │
            │     discord_link_codes, discord_channels,     │
            │     discord_rsvps                             │
            └───────────────▲─────────────────────────────┘
                            │ pgx (shares stores)
            ┌───────────────┴─────────────────────────────┐
            │  bot (Go, discordgo) — optional, no inbound  │
            │   • /coreteam slash command (link/setup/     │
            │     post/signup/status/unset) + details DM,    │
            │     RSVPs, and DM signup intake               │
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
  `EncounterStore`, `GroupingStore`, `MemberStore` (the member pool /
  recruitment roster), `RefreshTokenStore`, `PasswordResetStore`,
  `DiscordStore` (link codes + channel bindings + RSVPs), and `SettingsStore`
  for the `app_settings` key/value store). ESO game reference data and the
  player-build validators live in `eso.go`, kept separate from the persistence
  stores.
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
  `groupings.go`, `members.go` (the member pool / recruitment roster),
  `discord.go` (Discord account-link codes + link status), `password_reset.go`
  (forgot/reset password), and admin-only user/settings handlers (with a
  `requireAdmin` gate) in `admin.go`.

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
| password_hash  | text          | bcrypt hash, not null (unusable sentinel for Discord-only accounts) |
| is_admin       | boolean       | default `false`; `015_admin_and_settings.sql` |
| discord_user_id  | text        | linked Discord user ID, unique when set; `027_discord.sql`; set by `/coreteam link` **or** Discord sign-up |
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
| encounters_enabled | boolean       | surface multi-encounter UI; default `false`; `017_team_encounters_enabled.sql` |
| post_footer    | text              | Discord bot `/coreteam post` footer (default `''`); `018_team_signup_note.sql` → `029_team_bot_footers.sql` |
| dm_footer      | text              | Discord bot build-details DM footer (default `''`); `019_team_detailed_header.sql` → `029_team_bot_footers.sql` |
| signup_post    | text              | Discord bot `/coreteam recruit` recruitment body (default `''`); `030_team_members_pool.sql` |
| auto_share_pool_viewers | boolean  | auto-grant viewer access to member-pool app accounts; default `false`; `034_team_auto_share_pool.sql` |
| pre_made       | boolean           | one-off pre-made trial run mode; default `false`; `035_team_premade.sql` |
| premade_post   | text              | body the bot prepends to a `/coreteam signup` post (default `''`); `035_team_premade.sql` |
| simple_signup  | boolean           | pre-made role-based "simple" signup (hides class/gear + details, claims first matching slot); default `true` (`051_…`, was `false` in `037_team_simple_signup.sql`); web UI shows it inverted as an "Advanced signup" toggle, off by default |
| waitlist_enabled | boolean         | pre-made per-role waitlist (auto-promote on freed slot); default `false`; `038_premade_waitlist.sql` |
| roles          | jsonb             | customizable roster roles `[{key,label}]`; default Tank/Healer/DPS/Support DPS; `042_team_roles.sql` |
| active_roster_id | bigint          | FK → `rosters(id)`, set null; the roster the bot uses + the app shows by default; `048_rosters.sql` |
| created_at     | timestamptz       | default `now()`                            |
| updated_at     | timestamptz       | auto-updated via trigger                   |

The trial schedule lives on `teams` (`004_team_schedule.sql`). `schedule_time`
is stored in UTC and converted to/from each viewer's current timezone in the
browser; the old per-team `schedule_timezone` column was removed in
`010_drop_schedule_timezone.sql`, and the `team_timezones` list of extra display
zones (`009_team_timezones.sql`) was removed in `031_drop_team_timezones.sql`
(viewers always see their own zone). `encounters_enabled` gates the
multi-encounter UI (off → only the first encounter shows); `post_footer` /
`dm_footer` are free-form footers the Discord bot appends to its `/coreteam post`
overview and its build-details DM respectively (see `docs/AGENT_CONTEXT.md`
"Discord bot footers"); `signup_post` is the free-form body the bot posts with
`/coreteam recruit` to recruit new members (see "Member pool" below).
`auto_share_pool_viewers`, when true, auto-grants viewer access (in
`team_members`) to the app accounts of everyone in the team's member pool —
current and future — once their Discord identity is tied to an account (see
`docs/AGENT_CONTEXT.md` "Auto-share with member pool"). `pre_made` turns the team
into a one-off **pre-made trial run** posted via the bot's `/coreteam signup`
command; `premade_post` is the free-form body prepended to that post (see
`premade_runs` below and `docs/AGENT_CONTEXT.md` "Pre-made trial runs").
`simple_signup` (only meaningful with `pre_made`) switches that run from
per-slot "specific" signups to role-based "simple" signups: the post hides each
slot's class/gear and the build-details dropdown, players pick a **role**, and
claiming takes the first open slot matching it. `waitlist_enabled` (also pre-made
only) lets players join a **per-role** waitlist once a role is full; when a slot
of that role frees up, the head of that role's queue is auto-promoted into it and
DM'd (see `premade_waitlist` below and `docs/AGENT_CONTEXT.md` "Pre-made trial
runs").

### `team_members` (sharing)

| column   | type        | notes                                       |
|----------|-------------|---------------------------------------------|
| team_id  | bigint      | FK → `teams(id)`, cascade; PK part          |
| user_id  | bigint      | FK → `users(id)`, cascade; PK part          |
| role     | varchar(20) | `owner`, `editor`, or `viewer`              |
| added_at | timestamptz | default `now()`                             |

The owner is stored here as a `role = 'owner'` row, so any access check is a
single membership lookup.

### `rosters`

A **roster** (`048_rosters.sql`) is a named lineup within a team. A team can have
several rosters (cap **50**, `models.MaxRostersPerTeam`) and always designates
exactly one as **active** (`teams.active_roster_id`). Each roster fully owns its
composition: its 12 `players`, its `encounters` (+ loadouts), and its
`groupings`. The Discord bot always uses the active roster; the web app can view,
edit, create, rename, copy, delete, and activate any roster.

| column     | type              | notes                              |
|------------|-------------------|------------------------------------|
| id         | bigint (IDENTITY) | primary key                        |
| team_id    | bigint            | FK → `teams(id)`, cascade          |
| name       | varchar(100)      | default `'Main'`                   |
| position   | int               | display order; default `0`         |
| created_at | timestamptz       | default `now()`                    |
| updated_at | timestamptz       | auto-updated via trigger           |

Existing teams were backfilled with a single `'Main'` roster (set active) that
owns all their prior players/encounters/groupings. Managed via `RosterStore`;
HTTP under `/api/teams/{id}/rosters` (list/create with optional `copy_from`,
get, rename, delete, and `…/activate`). The roster-scoped collection endpoints
(`players/{slot}`, `encounters`, `groupings`) take an optional `?roster_id=`
query and default to the active roster when omitted. Templates (pre-made runs)
are locked to the active roster — the roster switcher is hidden in pre-made mode.

### `players`

| column         | type         | notes                                    |
|----------------|--------------|------------------------------------------|
| id             | bigint (IDENTITY) | primary key                         |
| roster_id      | bigint       | FK → `rosters(id)`, cascade; `048_rosters.sql` (was `team_id`) |
| slot           | smallint     | 1–12, unique per roster                  |
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

Every roster is created with all 12 player slots pre-populated (in a single
transaction), so slots are edited rather than added/removed. New slots default
their roles to 2 tanks / 2 healers / 8 dps. Players belong to a **roster** (not
the team directly) since `048_rosters.sql`; `teams.active_roster_id` selects
which roster's lineup the bot and the default web view use. Class, skill-line, and mastery
values are validated against allow-lists in the backend
(`internal/models/eso.go`). Roles are **per-team** (`teams.roles` JSONB, migration
`042`): each player's role is validated against its team's own role set rather
than a global list. Subclassing (`006_player_subclass.sql`) is
mutually exclusive: when `subclassed` is true the three `skill_line_*` columns
apply and the masteries are blanked; when false the two `mastery_*` columns
apply (drawn from the player's class) and the skill lines are blanked.

### `encounters` + `encounter_loadouts`

`encounters` (`007_encounters.sql`) holds per-roster named fights:

| column     | type              | notes                                 |
|------------|-------------------|---------------------------------------|
| id         | bigint (IDENTITY) | primary key                           |
| roster_id  | bigint            | FK → `rosters(id)`, cascade; `048_rosters.sql` (was `team_id`) |
| name       | varchar(100)      | `Default`, `Trash`, or a trial boss   |
| position   | int               | ordering within the roster            |
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
| splintered_secrets_skills | integer | slotted Herald of the Tome abilities 0–5 for Splintered Secrets pen (default `2`); `025_loadout_splintered_secrets_skills.sql` |
| force_of_nature_status | integer | negative status effects on enemy 0–5 for Force of Nature CP pen (default `5`); `026_loadout_force_of_nature_status.sql` |
| scribed_buffs | text[]   | group buffs a player's scribed (grimoire) skill provides (default `{}`); counted toward Group Buffs coverage; `032_loadout_scribed_buffs.sql` |
| banner_bearer_focus | text | Focus Script chosen for the Banner Bearer grimoire (default `''`); informational only; `033_loadout_banner_bearer_focus.sql` |

Every roster has at least one encounter (`Default`), created with the roster and
backfilled for existing teams. Encounter names are validated against
`ValidEncounterNames` and must be **unique per roster** and all from a **single
trial** (plus the always-allowed `General` group) — see
`ValidateEncounterSelection`, with a unique index on `encounters(roster_id, name)`
as a DB backstop. gear/skill/potion/cp_blue/crit_dmg/pen_extra/scribed_buffs
items are free-form (sanitized, not allow-listed); mundus and banner_bearer_focus
are trimmed strings and the armor counts are clamped to 0–7 — the searchable
options + tooltips live in the frontend master data (`frontend/js/data.js`).
`scribed_buffs` records the group buffs a player's scribed (grimoire) skill
provides and is counted toward the Group Buffs coverage; `banner_bearer_focus`
records the Banner Bearer grimoire's chosen Focus Script (informational only).
The crit-input columns
(`cp_blue`/`crit_dmg`/`mundus`/armor) feed the client-side crit-damage calculator
(see `docs/AGENT_CONTEXT.md` "Crit damage model"); `pen_extra` plus those reused
inputs feed the penetration calculator (see "Penetration model"); `players.race`
(`013_player_race.sql`) is the roster-level input both also read.

### `groupings` + `grouping_groups` + `grouping_members`

A **grouping** (`020_groupings.sql`) splits a roster into numbered groups
for mechanics (e.g. ice cages, slayer stacks). A roster may have several; a player
belongs to at most one group per grouping.

`groupings`:

| column      | type              | notes                                |
|-------------|-------------------|--------------------------------------|
| id          | bigint (IDENTITY) | primary key                          |
| roster_id   | bigint            | FK → `rosters(id)`, cascade; `048_rosters.sql` (was `team_id`) |
| name        | varchar(100)      | default `'Grouping'`                 |
| group_count | int               | 1–12 (CHECK), default `2`            |
| position    | int               | ordering within the roster           |
| created_at  | timestamptz       | default `now()`                      |
| updated_at  | timestamptz       | auto-updated via trigger             |

`grouping_groups` holds each numbered group's optional name (PK
`(grouping_id, group_number)`; blank → UI shows "Group N"). `grouping_members`
holds slot assignments with PK `(grouping_id, player_slot)`, which guarantees a
player is in at most one group per grouping. Both cascade on grouping delete.
Managed via `GroupingStore`; see `docs/AGENT_CONTEXT.md` "Groupings model".

### `roster_images` (positioning images)

Fight-positioning reference screenshots (`056_roster_images.sql`) attached to a
roster. The web app uploads them (bottom "Images" section) and the Discord bot
posts them — with their captions — into a run's discussion thread when the thread
is created (shared `postPositioningImages`): both the pre-made run / advanced
signup thread (`createRunThread`) and the `/coreteam post` overview thread
(`startPostThread`), so players know where to stand. Bytes are stored inline as
`bytea` (the backend container has no persistent volume; Postgres persists via
`db-data`). Capped at **5 MB/image** and **10/roster** (`handlers.maxImageBytes`
/ `maxRosterImagesPerRoster`).

| column       | type              | notes                                        |
|--------------|-------------------|----------------------------------------------|
| id           | bigint (IDENTITY) | primary key                                  |
| roster_id    | bigint            | FK → `rosters(id)`, cascade                  |
| position     | int               | display / post order within the roster       |
| caption      | varchar(200)      | optional per-image label (default `''`)      |
| content_type | text              | sniffed MIME (png/jpeg/gif/webp only)        |
| byte_size    | int               | stored so listings avoid reading the blob    |
| data         | bytea             | the raw image bytes                          |
| created_at   | timestamptz       | default `now()`                              |
| updated_at   | timestamptz       | set on caption update                        |

Managed via `RosterImageStore`. HTTP under `/api/teams/{id}/images`: list +
multipart `POST` (upload), `PUT {imgID}` (caption), `DELETE {imgID}`, and
`GET {imgID}/raw` (bearer-protected bytes — the frontend fetches with auth and
renders via an object URL). Uploads get a larger body cap in both nginx
(`client_max_body_size 6m` on `^/api/teams/[0-9]+/images`) and the backend
(`maxImageUploadBody`). Copied with a roster (`copyRosterImagesTx`). Hidden for
simple-signup templates.

### `team_roster_members` (member pool)

A per-team list of prospective players (`030_team_members_pool.sql`), **separate**
from the 12 fixed `players` slots and from app-account sharing (`team_members`).
It captures availability/role/class interest gathered via the Discord
`/coreteam recruit` DM intake, plus manual web entries.

| column           | type        | notes                                          |
|------------------|-------------|------------------------------------------------|
| id               | bigint (IDENTITY) | primary key                              |
| team_id          | bigint      | FK → `teams(id)`, cascade                      |
| discord_user_id  | text        | linked Discord user (NULL for manual entries)  |
| discord_username | text        | Discord display name (default `''`)            |
| display_name     | text        | shown name (default `''`)                      |
| timezone         | text        | IANA zone the availability hours are in        |
| days             | text[]      | available weekday keys `mon..sun` (default `{}`)|
| availability     | jsonb       | `{ "mon": { "start": 18, "end": 22 } }` (start 0–23, end 1–24; 24 = end of day) |
| roles            | text[]      | roles of interest (default `{}`)               |
| classes_by_role  | jsonb       | `{ "tank": ["dragonknight"] }`                 |
| status           | text        | `draft` (intake in progress) → `complete`      |
| step             | text        | current DM-intake step                         |
| source           | text        | `discord` or `manual`                          |
| created_at       | timestamptz | default `now()`                                |
| updated_at       | timestamptz | auto-updated via trigger                       |

A partial unique index on `(team_id, discord_user_id)` means re-running signup
**updates** the same row; manual entries (NULL `discord_user_id`) are
unconstrained. Managed via `MemberStore`; see `docs/AGENT_CONTEXT.md`
"Member pool / recruitment".

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

`discord_rsvps` (`028_discord_rsvps.sql`) records attendance from the ✅/❌
buttons on a posted overview — one row per `(message_id, discord_user_id)`:

| column              | type        | notes                                       |
|---------------------|-------------|---------------------------------------------|
| message_id          | text        | posted overview message (PK part)           |
| channel_id          | text        | channel the post is in                      |
| discord_user_id     | text        | the responder (PK part)                     |
| discord_username    | text        | responder's username (for slot matching)    |
| discord_global_name | text        | responder's display name (`050_…`)          |
| status              | text        | `'yes'` (coming) or `'no'` (not coming)     |
| created_at          | timestamptz | default `now()`                             |
| updated_at          | timestamptz | bump on re-RSVP                             |

Both names are stored so the post's inline ✅/❌ marks can match a responder to a
roster slot whose `discord_handle` is set to either their username or their
display name (mirroring the live user the "Get My Build Details" button uses).

Managed via `DiscordStore`; the hourly sweep prunes expired/used link codes. See
`docs/AGENT_CONTEXT.md` "Discord bot".

`discord_posts` (`049_discord_posts.sql`) — one row per posted `/coreteam post`
overview, so the scheduler can ping attendees in the post's discussion thread
~15 min before the run. Keyed by `message_id` (re-posting starts fresh):

| column     | type        | notes                                              |
|------------|-------------|----------------------------------------------------|
| message_id | text        | posted overview message (PK)                       |
| channel_id | text        | channel the post is in                             |
| thread_id  | text        | discussion thread opened off the post (`''` until set) |
| run_at     | timestamptz | next-run time from the team schedule; NULL ⇒ no ping |
| pinged_at  | timestamptz | set once the pre-run ping has fired (once-only)    |
| created_at | timestamptz | default `now()`                                    |

`premade_runs` (`036_premade_runs.sql`) — one row per posted **pre-made trial
run**. The bookkeeping timestamps drive the bot's scheduler (thread 15 min
before, cleanup 2 h after) and make those actions fire exactly once / catch up
after a restart:

| column            | type        | notes                                       |
|-------------------|-------------|---------------------------------------------|
| id                | bigint      | primary key                                 |
| team_id           | bigint      | FK → `teams(id)`, cascade                   |
| guild_id          | text        | guild the run was posted in                 |
| channel_id        | text        | channel the post is in                      |
| message_id        | text        | the announcement message (set after posting)|
| thread_id         | text        | the run thread (set 15 min before)          |
| title             | text        | run title from the DM conversation          |
| post_override     | text        | per-run body overriding `premade_post` (default `''`); `040_dm_signup.sql` |
| scheduled_at      | timestamptz | trial start, **UTC** (from runner's zone)   |
| created_by        | bigint      | FK → `users(id)`, set null on delete        |
| thread_started_at | timestamptz | NULL until the thread is created            |
| cleaned_up_at     | timestamptz | NULL until the post/thread are deleted      |
| cleanup_attempts  | integer     | failed cleanup attempts (backoff); `055_premade_cleanup_backoff.sql` |
| cleanup_next_at   | timestamptz | earliest next cleanup retry (NULL = now); `055` |
| cleanup_failed_at | timestamptz | set when cleanup retries are exhausted (stops retrying); `055` |
| created_at        | timestamptz | default `now()`                             |
| updated_at        | timestamptz | auto-updated via trigger                    |

`premade_signups` (`036_premade_runs.sql`) — one row per claimed slot. A unique
`(run_id, slot)` locks a slot to one claimant; a unique `(run_id, discord_user_id)`
enforces one slot per user (switching releases the prior claim in the same tx):

| column           | type        | notes                                     |
|------------------|-------------|-------------------------------------------|
| id               | bigint      | primary key                               |
| run_id           | bigint      | FK → `premade_runs(id)`, cascade          |
| slot             | smallint    | 1–12; unique per run                      |
| discord_user_id  | text        | claimant; unique per run                  |
| discord_username | text        | display name at claim time                |
| created_at       | timestamptz | default `now()`                           |

`premade_waitlist` (`038_premade_waitlist.sql`) — one row per user waiting for a
**role** on a run (only used when the team's `waitlist_enabled` is on). A unique
`(run_id, discord_user_id)` enforces one waitlist entry per user; `created_at`
orders each role's FIFO queue. When a claimed slot is freed, `PromoteToSlot`
moves the head of that slot's role queue into the open slot (transactionally,
`FOR UPDATE SKIP LOCKED`) and the bot DMs them:

| column           | type        | notes                                     |
|------------------|-------------|-------------------------------------------|
| id               | bigint      | primary key                               |
| run_id           | bigint      | FK → `premade_runs(id)`, cascade          |
| role             | text        | role they're waiting for                  |
| discord_user_id  | text        | waiter; unique per run                    |
| discord_username | text        | display name at join time                 |
| created_at       | timestamptz | default `now()`; FIFO order               |

`premade_signup_sessions` (`040_dm_signup.sql`) — one in-progress **DM signup
conversation** per Discord user (primary key `discord_user_id`), persisted so a
half-finished signup survives a bot restart. `step` names the awaited answer
(create flow: team / tz / title / when / confirm / body; edit flow:
edit_field / edit_title / edit_when / edit_body / edit_signup_name /
edit_signup_pick / edit_signup_slot); a new `/coreteam signup` or
**Edit run** press overwrites any prior session, and the flow deletes it on
completion (`finishPremadeDM` / the edit "Done" choice). Typing a cancel word
(`isCancel`: cancel / stop / quit / abort / exit / nevermind) in the DM at any
step deletes the session and aborts the conversation:

| column           | type        | notes                                                        |
|------------------|-------------|--------------------------------------------------------------|
| discord_user_id  | text        | primary key (the runner)                                     |
| app_user_id      | bigint      | FK → `users(id)`, cascade                                    |
| team_id          | bigint      | FK → `teams(id)`, cascade; NULL until chosen                 |
| guild_id         | text        | guild the signup was started in                              |
| channel_id       | text        | channel to post the run in                                   |
| dm_channel_id    | text        | the DM channel driving the conversation                      |
| step             | text        | the awaited answer                                           |
| title            | text        | run title (once entered)                                     |
| scheduled_at     | timestamptz | parsed run time, **UTC** (NULL until set)                    |
| post_override    | text        | optional per-run body (default `''`)                         |
| mode             | text        | `''` create flow / `'edit'` editing a run; `041_premade_run_edit.sql` |
| run_id           | bigint      | FK → `premade_runs(id)`, cascade; the run being edited (`041`) |
| signup_user_id   | text        | Discord user ID of the "sign up a player" target (default `''`); `''` for a free-typed name with no Discord match; `047_premade_signup_target.sql` |
| signup_user_name | text        | display name of the signup target parked between the pick and slot steps (default `''`); `047_premade_signup_target.sql` |
| created_at       | timestamptz | default `now()`                                              |
| updated_at       | timestamptz | bumped on each answer                                        |

The runner's remembered timezone lives in `users.timezone` (`040_dm_signup.sql`,
default `''`): asked once via a DM select, then reused so natural-language times
resolve without re-asking. Users can set or change it at any time with
`/coreteam timezone` (`handleTimezone`/`handleTimezoneSelect`, ephemeral select).

Managed via `PremadeStore`; the bot's background scheduler (`cmd/bot/scheduler.go`,
the only time-based worker) acts on due runs. See `docs/AGENT_CONTEXT.md`
"Pre-made trial runs".

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
